import '@testing-library/jest-dom'
import i18n from '../i18n'

// The console ships with Chinese as its default language, but these suites assert
// English UI text. Pinning the language here keeps every one of them independent
// of that product decision, so flipping the default does not require rewriting
// assertions that were never about language in the first place.
//
// The default itself is covered on purpose in i18n/__tests__/lang.test.ts, and
// SettingsLanguage.test.tsx switches languages explicitly.
await i18n.changeLanguage('en')

if (!Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = () => {}
}
