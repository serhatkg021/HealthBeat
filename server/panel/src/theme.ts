// Panel teması: kullanıcı "Sistem" (işletim sisteminin ayarı), "Açık" ya da "Koyu" seçer; seçim bu tarayıcıda saklanır
// (hesaba yazılmaz). Görünen tema kök elemanın data-theme özniteliğidir; ilk boyamadan önce public/theme.js aynı kuralla
// yazar. React yok: Node'un çalıştırıcısıyla birim test edilir.

export type ThemeChoice = 'system' | 'light' | 'dark'
export type Theme = 'light' | 'dark'

// public/theme.js ile aynı anahtar.
export const THEME_KEY = 'healthbeat_theme'

export const THEME_CHOICES: { value: ThemeChoice; label: string }[] = [
  { value: 'system', label: 'Sistem' },
  { value: 'light', label: 'Açık' },
  { value: 'dark', label: 'Koyu' },
]

export const parseChoice = (raw: string | null | undefined): ThemeChoice => (raw === 'light' || raw === 'dark' ? raw : 'system')

export const resolveTheme = (choice: ThemeChoice, systemDark: boolean): Theme =>
  choice === 'system' ? (systemDark ? 'dark' : 'light') : choice

// Üst çubuktaki simgenin sırası: Sistem → Açık → Koyu → Sistem.
export function nextChoice(choice: ThemeChoice): ThemeChoice {
  const i = THEME_CHOICES.findIndex((c) => c.value === choice)
  return THEME_CHOICES[(i + 1) % THEME_CHOICES.length].value
}

export const choiceLabel = (choice: ThemeChoice): string => THEME_CHOICES.find((c) => c.value === choice)?.label ?? choice
