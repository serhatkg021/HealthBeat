import { Monitor, Moon, Sun } from 'lucide-react'
import { choiceLabel, nextChoice, THEME_CHOICES } from '../theme'
import { useTheme } from './useTheme'

const ICON = { system: Monitor, light: Sun, dark: Moon } as const

// Üst çubuktaki (ve giriş sayfasındaki) tema simgesi: her tıklama sıradaki seçeneğe geçer (Sistem → Açık → Koyu).
export function ThemeToggle() {
  const [choice, setChoice] = useTheme()
  const Icon = ICON[choice]
  const next = nextChoice(choice)
  return (
    <button
      type="button"
      className="icon-btn theme-toggle"
      onClick={() => setChoice(next)}
      title={`Tema: ${choiceLabel(choice)} (tıklayınca ${choiceLabel(next)})`}
      aria-label={`Tema: ${choiceLabel(choice)}. ${choiceLabel(next)} temasına geç`}
    >
      <Icon size={16} strokeWidth={1.9} />
    </button>
  )
}

// Profil menüsündeki üç seçenekli tema satırı.
export function ThemeChoices() {
  const [choice, setChoice] = useTheme()
  return (
    <div className="segmented theme-choices" role="group" aria-label="Tema">
      {THEME_CHOICES.map((c) => {
        const Icon = ICON[c.value]
        return (
          <button key={c.value} type="button" aria-pressed={choice === c.value} onClick={() => setChoice(c.value)}>
            <Icon size={14} strokeWidth={1.9} aria-hidden="true" />
            {c.label}
          </button>
        )
      })}
    </div>
  )
}
