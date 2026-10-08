import type { TFunction } from 'i18next'

/**
 * Which of the one-liner's known failures a backend message is.
 *
 * The backend reports its failures in English because it is also an API: the
 * same sentence is read by a script and by an operator. A script can act on
 * "listener 1 was not started by this console, so the website it serves is
 * unknown", and an operator cannot -- it is a paragraph about Sliver's
 * internals that never says what to do next.
 *
 * Recognising the sentence and replacing it with a translated instruction is
 * the smallest change that fixes that. The alternative -- a structured error
 * code on every response -- changes the API for every caller, and the console
 * already localises the two most common one-liner failures this way.
 *
 * The match is deliberately narrow: anything not recognised is passed through
 * untouched, because inventing a friendly sentence for a real fault would hide
 * it.
 */
export type OneLinerErrorKind = 'pick' | 'hostNeeded' | 'siteUnknown' | 'raw'

export function classifyOneLinerError(message: string): OneLinerErrorKind {
  // The listener was stopped between the last poll and this click, so the reply
  // names a job id that is still on screen.
  if (/no listener with job id/i.test(message)) return 'pick'
  // The listener is bound to a wildcard or loopback address, so no callback
  // address can be derived for a target elsewhere.
  if (/no address a target can reach/i.test(message)) return 'hostNeeded'
  // The listener exists but this console did not start it, so the website it
  // serves -- the only place a stage can be published -- is not recorded.
  if (/was not started by this console/i.test(message)) return 'siteUnknown'
  return 'raw'
}

/**
 * The message to show the operator for a backend failure.
 *
 * The context matters for one case: "no listener with job id N" means "choose a
 * listener" in the panel, where a picker is on screen, and "the listener you
 * opened is gone" in the dialog, which has no picker and cannot offer one.
 */
export function oneLinerErrorText(
  message: string,
  t: TFunction,
  context: 'panel' | 'dialog' = 'panel',
): string {
  switch (classifyOneLinerError(message)) {
    case 'pick':
      return context === 'dialog' ? t('oneliner.listenerGone') : t('oneliner.pickListener')
    case 'hostNeeded':
      return t('oneliner.hostNeeded')
    case 'siteUnknown':
      return t('oneliner.siteUnknown')
    default:
      return message
  }
}
