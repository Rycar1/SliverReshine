import { describe, it, expect } from 'vitest'
import { classifyOneLinerError, oneLinerErrorText } from '../oneLinerError'
import type { TFunction } from 'i18next'

const strings: Record<string, string> = {
  'oneliner.pickListener': 'Pick a listener first',
  'oneliner.listenerGone': 'This listener is no longer running',
  'oneliner.hostNeeded': 'Type an address the target can reach',
  'oneliner.siteUnknown': 'This listener was not started from this console',
}
const t = ((key: string) => strings[key] ?? key) as unknown as TFunction

describe('classifyOneLinerError', () => {
  it('recognises a listener that has gone away', () => {
    expect(classifyOneLinerError('no listener with job id 4')).toBe('pick')
  })

  it('recognises a listener with no derivable callback address', () => {
    expect(classifyOneLinerError('listener 3 has no address a target can reach')).toBe('hostNeeded')
  })

  it('recognises a listener this console did not start', () => {
    expect(
      classifyOneLinerError(
        'listener 1 was not started by this console, so the website it serves is unknown.',
      ),
    ).toBe('siteUnknown')
  })

  it('leaves an unrecognised message raw', () => {
    expect(classifyOneLinerError('implant profile "x" not found')).toBe('raw')
  })
})

describe('oneLinerErrorText', () => {
  it('offers the picker in the panel', () => {
    expect(oneLinerErrorText('no listener with job id 4', t, 'panel')).toBe('Pick a listener first')
  })

  it('explains the missing listener in the dialog, which has no picker', () => {
    expect(oneLinerErrorText('no listener with job id 4', t, 'dialog')).toBe(
      'This listener is no longer running',
    )
  })

  it('explains an address that cannot be derived, in either context', () => {
    const raw = 'listener 3 has no address a target can reach'
    expect(oneLinerErrorText(raw, t, 'panel')).toBe('Type an address the target can reach')
    expect(oneLinerErrorText(raw, t, 'dialog')).toBe('Type an address the target can reach')
  })

  it('replaces the backend paragraph about an unknown website with an instruction', () => {
    const raw =
      'listener 1 was not started by this console, so the website it serves is unknown. Sliver does not report it: a stage published to the wrong website is invisible to the listener and the delivery URL returns 404. Start the listener from here, or use the WebDelivery page and name the website explicitly'
    expect(oneLinerErrorText(raw, t, 'panel')).toBe('This listener was not started from this console')
    expect(oneLinerErrorText(raw, t, 'dialog')).toBe('This listener was not started from this console')
  })

  it('passes an unrecognised message through unchanged', () => {
    expect(oneLinerErrorText('implant profile "x" not found', t)).toBe(
      'implant profile "x" not found',
    )
  })
})
