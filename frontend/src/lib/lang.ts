import i18n, { LANG_KEY } from '../i18n'

export type Lang = 'en' | 'zh'

export const LANGS: { value: Lang; label: string; native: string }[] = [
  { value: 'en', label: 'English', native: 'English' },
  { value: 'zh', label: 'Chinese', native: '中文' },
]

/**
 * The active language, normalised to a value this app actually ships.
 *
 * The comparison is against 'en' rather than against 'zh' so that anything
 * unrecognised -- a stale tag, a regional variant, a typo -- lands on Chinese,
 * the bundle the console is built in, instead of on the translation.
 */
export function currentLang(): Lang {
  return i18n.language === 'en' ? 'en' : 'zh'
}

/**
 * Switch language and persist the choice.
 *
 * Single writer for the persisted language: both the sidebar toggle and the
 * Settings page call this, so they cannot drift apart — which is what broke
 * persistence before. Storage failures (private mode, quota) are ignored
 * because the in-memory switch still works for the session.
 */
export function setLang(next: Lang): void {
  i18n.changeLanguage(next)
  try {
    localStorage.setItem(LANG_KEY, next)
  } catch {
    /* ignore */
  }
}
