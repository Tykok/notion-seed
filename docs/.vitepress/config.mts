import { defineConfig } from 'vitepress'

const repo = 'https://github.com/tykok/notion-seed'

export default defineConfig({
  title: 'notion-seed',
  base: '/notion-seed/',
  cleanUrls: true,
  head: [
    ['link', { rel: 'preconnect', href: 'https://fonts.googleapis.com' }],
    ['link', { rel: 'preconnect', href: 'https://fonts.gstatic.com', crossorigin: '' }],
    [
      'link',
      {
        rel: 'stylesheet',
        href: 'https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600&family=JetBrains+Mono:wght@400;500&display=swap',
      },
    ],
  ],
  themeConfig: {
    socialLinks: [{ icon: 'github', link: repo }],
    search: {
      provider: 'local',
      options: {
        locales: {
          fr: {
            translations: {
              button: { buttonText: 'Rechercher', buttonAriaLabel: 'Rechercher' },
              modal: {
                noResultsText: 'Aucun résultat pour',
                resetButtonTitle: 'Effacer la recherche',
                footer: { selectText: 'choisir', navigateText: 'naviguer', closeText: 'fermer' },
              },
            },
          },
        },
      },
    },
  },
  locales: {
    root: {
      label: 'English',
      lang: 'en',
      description: 'Declare your Notion databases in YAML, and see in rows what each change costs before anything is written.',
      themeConfig: {
        nav: [{ text: 'Docs', link: '/installation' }],
        sidebar: [
          {
            text: 'Guide',
            items: [
              { text: 'Overview', link: '/' },
              { text: 'Installation', link: '/installation' },
              { text: 'Commands', link: '/commands' },
              { text: 'YAML', link: '/yaml' },
            ],
          },
        ],
        outline: { level: [2, 3], label: 'On this page' },
        editLink: { pattern: `${repo}/edit/main/docs/:path`, text: 'Edit this page on GitHub' },
      },
    },
    fr: {
      label: 'Français',
      lang: 'fr',
      link: '/fr/',
      description: 'Déclarez vos bases Notion en YAML, et voyez en nombre de lignes ce que coûte chaque changement avant toute écriture.',
      themeConfig: {
        nav: [{ text: 'Docs', link: '/fr/installation' }],
        sidebar: [
          {
            text: 'Guide',
            items: [
              { text: 'Présentation', link: '/fr/' },
              { text: 'Installation', link: '/fr/installation' },
              { text: 'Commandes', link: '/fr/commands' },
              { text: 'YAML', link: '/fr/yaml' },
            ],
          },
        ],
        outline: { level: [2, 3], label: 'Sur cette page' },
        editLink: { pattern: `${repo}/edit/main/docs/:path`, text: 'Modifier cette page sur GitHub' },
        docFooter: { prev: 'Page précédente', next: 'Page suivante' },
        darkModeSwitchLabel: 'Apparence',
        sidebarMenuLabel: 'Menu',
        returnToTopLabel: 'Retour en haut',
        langMenuLabel: 'Changer de langue',
      },
    },
  },
})
