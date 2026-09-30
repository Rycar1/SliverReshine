import '@testing-library/jest-dom'
import '../i18n'

if (!Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = () => {}
}
