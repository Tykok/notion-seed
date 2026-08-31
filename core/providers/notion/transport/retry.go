package transport

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"time"
)

// RetryPolicy borne les tentatives. Seuls 429 et 5xx sont rejoués.
type RetryPolicy struct {
	MaxAttempts int
	Base        time.Duration
	Max         time.Duration
	Jitter      func(time.Duration) time.Duration
}

func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxAttempts: 4,
		Base:        500 * time.Millisecond,
		Max:         30 * time.Second,
		Jitter:      fullJitter,
	}
}

// fullJitter tire uniformément dans [0, d] : évite que plusieurs workers
// rejouent en phase après un 429 commun.
func fullJitter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	return time.Duration(rand.Int63n(int64(d)) + 1)
}

// MaxRetryAfterWait plafonne l'attente qu'un serveur peut imposer via
// Retry-After. Le header garde la priorité sur le backoff calculé — le serveur
// sait mieux que nous quand il acceptera le prochain appel — mais « prioritaire
// sur la valeur calculée » ne veut pas dire « exempté de tout plafond absolu ».
//
// Sans plafond, un `retry-after: 60` — parfaitement plausible d'un vrai
// limiteur — bloquerait un `plan` en LECTURE SEULE trois minutes en CI, et un
// header hostile bloquerait indéfiniment. La valeur retenue est celle du
// plafond du backoff calculé (RetryPolicy.Max par défaut, 30 s) : deux
// plafonds différents pour la même question — « combien de temps accepte-t-on
// d'attendre entre deux tentatives » — se justifieraient mal.
//
// L'échéance restante du contexte n'a pas été retenue comme plafond : au MVP 0,
// le contexte de plan n'en porte aucune (le timeout est posé par appel dans
// NtnShell), donc elle ne bornerait rien.
const MaxRetryAfterWait = 30 * time.Second

// Retrying décore un Transport avec le rate limiter et la politique de retry.
// Le limiter est consulté avant chaque tentative, y compris les rejouées.
type Retrying struct {
	inner   Transport
	limiter Limiter
	policy  RetryPolicy
	clock   Clock
	notify  func(string)
}

// NewRetrying panique si policy.MaxAttempts est inférieur à 1, même contrat que
// NewTokenBucket. Sans cette garde, une RetryPolicy à sa valeur zéro fait que la
// boucle d'Execute ne tourne jamais et rend (APIResponse{}, nil) : un faux
// succès silencieux, sans qu'aucun appel n'ait été émis. Un apply construirait
// son state là-dessus.
//
// notify reçoit une ligne par attente. Il peut être nil, mais une CLI ne doit
// pas le laisser nil : une attente muette est indistinguable d'un blocage.
func NewRetrying(inner Transport, limiter Limiter, policy RetryPolicy, clock Clock, notify func(string)) *Retrying {
	if policy.MaxAttempts < 1 {
		panic(fmt.Sprintf("NewRetrying: policy.MaxAttempts doit être >= 1, reçu %d", policy.MaxAttempts))
	}
	if clock == nil {
		clock = RealClock{}
	}
	if policy.Jitter == nil {
		policy.Jitter = fullJitter
	}
	return &Retrying{inner: inner, limiter: limiter, policy: policy, clock: clock, notify: notify}
}

func (r *Retrying) Execute(ctx context.Context, req APIRequest) (APIResponse, error) {
	var lastResp APIResponse
	var lastErr error

	for attempt := 0; attempt < r.policy.MaxAttempts; attempt++ {
		if r.limiter != nil {
			if err := r.limiter.Wait(ctx); err != nil {
				return APIResponse{}, err
			}
		}

		resp, err := r.inner.Execute(ctx, req)
		if err == nil {
			return resp, nil
		}
		lastResp, lastErr = resp, err

		// Une issue inconnue ne se rejoue jamais : la mutation a peut-être
		// abouti, la rejouer risque de la dupliquer.
		var unknown *OutcomeUnknownError
		if errors.As(err, &unknown) {
			return resp, err
		}
		// Un appel mal construit est un bug interne, pas un incident transitoire.
		var usageErr *UsageError
		if errors.As(err, &usageErr) {
			return resp, err
		}
		var apiErr *APIError
		if !errors.As(err, &apiErr) || !apiErr.Retryable() {
			return resp, err
		}
		if attempt == r.policy.MaxAttempts-1 {
			break
		}

		wait, source := r.backoff(attempt, resp)
		r.announce(wait, source)
		if err := r.clock.Sleep(ctx, wait); err != nil {
			return resp, err
		}
	}
	return lastResp, lastErr
}

// announce dit à l'utilisateur qu'on attend, et pourquoi. Une attente de
// plusieurs dizaines de secondes sans une ligne de sortie est indistinguable
// d'un blocage : mesuré, un `retry-after: 3` produisait 9,35 s de silence total.
func (r *Retrying) announce(d time.Duration, source string) {
	if r.notify == nil || d <= 0 {
		return
	}
	r.notify(fmt.Sprintf("en attente %s avant nouvelle tentative (%s)",
		d.Round(time.Millisecond), source))
}

// backoff privilégie toujours Retry-After sur le calcul interne : le serveur
// sait mieux que nous quand il acceptera le prochain appel. Il reste plafonné
// par MaxRetryAfterWait. La seconde valeur rendue nomme la source de l'attente,
// pour que le message affiché le dise.
func (r *Retrying) backoff(attempt int, resp APIResponse) (time.Duration, string) {
	if d, ok := resp.RetryAfter(); ok {
		if d > MaxRetryAfterWait {
			return MaxRetryAfterWait, fmt.Sprintf(
				"Retry-After %s, plafonné à %s", d, MaxRetryAfterWait)
		}
		return d, "Retry-After"
	}
	d := time.Duration(float64(r.policy.Base) * math.Pow(2, float64(attempt)))
	// Une policy pathologique (Base énorme, ou beaucoup de tentatives) peut faire
	// sortir la conversion float64 -> int64 du domaine. Le résultat est alors
	// dépendant de l'architecture : amd64 rend un négatif, arm64 sature vers le
	// positif. Un négatif contournerait le clamp ci-dessous et dégénérerait en
	// sleep nul, donc en retry immédiat.
	if d < 0 || d > r.policy.Max {
		d = r.policy.Max
	}
	return r.policy.Jitter(d), "backoff"
}
