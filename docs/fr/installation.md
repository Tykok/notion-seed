# Installation

## Prérequis

`notion-seed` ne gère pas l'authentification : il délègue les appels à
[`ntn`](https://www.npmjs.com/package/ntn), qui stocke le jeton dans le
keychain de l'OS.

```sh
npm i -g ntn@0.22.11
ntn login
```

::: warning La version est épinglée
`notion-seed` refuse de tourner en dessous de `ntn` 0.22.11 : c'est la seule
version sur laquelle le format de sortie de `ntn` a été mesuré.
:::

## Installer notion-seed

Binaire publié — Linux, macOS et Windows, en amd64 et arm64 — depuis la page
[Releases](https://github.com/tykok/notion-seed/releases) :

```sh
# macOS arm64, à adapter à votre plateforme
curl -fsSL https://github.com/tykok/notion-seed/releases/latest/download/notion-seed_<version>_darwin_arm64.tar.gz \
  | tar -xz notion-seed
```

### macOS, Linux (Homebrew)

```sh
brew install tykok/tap/notion-seed
```

### Debian, Ubuntu

apt n'a pas de mécanisme de tap par utilisateur, mais un dépôt apt n'est qu'un
arbre de fichiers statiques : celui-ci est hébergé sur GitHub Pages et signé.

```sh
sudo install -d /etc/apt/keyrings
curl -fsSL https://tykok.github.io/notion-seed/apt/gpg.key \
  | sudo tee /etc/apt/keyrings/notion-seed.asc > /dev/null
echo "deb [signed-by=/etc/apt/keyrings/notion-seed.asc] https://tykok.github.io/notion-seed/apt stable main" \
  | sudo tee /etc/apt/sources.list.d/notion-seed.list > /dev/null
sudo apt update && sudo apt install notion-seed
```

`signed-by` restreint la clé à ce seul dépôt : sans lui, la clé vaudrait pour
toutes les sources apt de la machine.

Un `.deb`, `.rpm` ou `.apk` est aussi attaché à chaque release, si vous préférez
ne rien ajouter aux sources :

```sh
sudo apt install ./notion-seed_<version>_linux_amd64.deb
```

### Depuis les sources

```sh
go install github.com/tykok/notion-seed@latest
```

Le binaire installé par `go install` annonce `0.0.0-dev` : la version n'est
injectée qu'au build de release.

## Démarrage

```sh
notion-seed init    # vérifie que ntn est présent, assez récent et authentifié
notion-seed plan    # affiche les changements
```

Ensuite : les [commandes](/fr/commands), puis comment décrire vos bases en [YAML](/fr/yaml).
