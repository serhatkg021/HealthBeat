import { useEffect, useSyncExternalStore } from 'react'
import { parseChoice, resolveTheme, THEME_KEY, type ThemeChoice } from '../theme'

// Tema seçimi tek yerde tutulur (birden çok düğme aynı anda güncellensin) ve bu tarayıcıda saklanır. localStorage okuma
// ve yazması her zaman try/catch'lidir: gizli pencerede ya da engellenmiş depolamada seçim yalnızca bu oturumda geçerlidir.
const listeners = new Set<() => void>()
let current: ThemeChoice = (() => {
  try {
    return parseChoice(localStorage.getItem(THEME_KEY))
  } catch {
    return 'system'
  }
})()

const systemQuery = () => window.matchMedia?.('(prefers-color-scheme: dark)')

function apply() {
  document.documentElement.setAttribute('data-theme', resolveTheme(current, systemQuery()?.matches ?? false))
}

function setChoice(choice: ThemeChoice) {
  current = choice
  try {
    if (choice === 'system') localStorage.removeItem(THEME_KEY)
    else localStorage.setItem(THEME_KEY, choice)
  } catch {
    // depolama yok: seçim bu oturumda geçerli
  }
  apply()
  listeners.forEach((l) => l())
}

const subscribe = (l: () => void) => {
  listeners.add(l)
  return () => listeners.delete(l)
}

export function useTheme(): [ThemeChoice, (choice: ThemeChoice) => void] {
  const choice = useSyncExternalStore(subscribe, () => current)
  // "Sistem" seçiliyken işletim sisteminin teması değişince panel de değişir.
  useEffect(() => {
    if (choice !== 'system') return
    const q = systemQuery()
    if (!q) return
    q.addEventListener('change', apply)
    return () => q.removeEventListener('change', apply)
  }, [choice])
  return [choice, setChoice]
}
