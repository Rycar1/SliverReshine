import i18n from 'i18next'
import { initReactI18next } from 'react-i18next'
import en from './locales/en'
import zh from './locales/zh'

/**
 * localStorage key holding the selected language.
 *
 * Exported so that every writer uses this exact constant. The language used to
 * be written under a second key (`sliverui-lang`) by the sidebar toggle while
 * this module read `c2tool-lang`, so switching language appeared to work and
 * then silently reverted on reload. Import this rather than retyping the string.
 */
export const LANG_KEY = 'c2tool-lang'

const saved = localStorage.getItem(LANG_KEY)
const initial = saved === 'zh' || saved === 'en' ? saved : 'en'

i18n.use(initReactI18next).init({
  resources: {
    en: { translation: en },
    zh: { translation: zh },
  },
  lng: initial,
  fallbackLng: 'en',
  interpolation: {
    escapeValue: false,
  },
})

export default i18n
