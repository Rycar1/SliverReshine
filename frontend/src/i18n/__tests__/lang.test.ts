import { describe, it, expect, beforeEach } from 'vitest'
import i18n, { LANG_KEY, DEFAULT_LANG } from '../index'
import { currentLang, setLang, LANGS } from '../../lib/lang'

describe('language preference', () => {
  beforeEach(async () => {
    localStorage.clear()
    await i18n.changeLanguage('en')
  })

  it('persists under a stable key so the choice survives a reload', () => {
    setLang('zh')

    // 回归测试：侧栏切换曾经写入 'sliverui-lang'、后来又写入 'c2tool-lang'，而
    // i18n/index.ts 初始化时读的是另一个 key，导致切完中文一刷新就回退成英文。
    // 这几个断言把「写入的 key」和「读取的 key」钉在一起，并确认旧 key 不再被写入。
    expect(localStorage.getItem(LANG_KEY)).toBe('zh')
    expect(localStorage.getItem('sliverreshine-lang')).toBe('zh')
    expect(localStorage.getItem('c2tool-lang')).toBeNull()
    expect(localStorage.getItem('sliverui-lang')).toBeNull()
  })

  it('currentLang tracks the active language', async () => {
    setLang('zh')
    await i18n.changeLanguage('zh')
    expect(currentLang()).toBe('zh')

    setLang('en')
    await i18n.changeLanguage('en')
    expect(currentLang()).toBe('en')
  })

  it('falls back to zh for an unsupported language tag', async () => {
    // currentLang 必须把任何非 en 的值收敛成 'zh'，否则设置页会两个按钮都不高亮。
    await i18n.changeLanguage('fr')
    expect(currentLang()).toBe('zh')
  })
  it('defaults to Chinese when nothing has been saved yet', () => {
    // 回归测试：新装的控制台必须直接是中文，而不是先英文再让用户去设置页切换。
    // 这里读的是 i18n/index.ts 里那份初始化逻辑的默认值，避免它被改回 'en'。
    expect(DEFAULT_LANG).toBe('zh')
  })

  it('every offered language has a translation bundle', () => {
    const bundles = Object.keys(i18n.options.resources ?? {})
    for (const l of LANGS) {
      expect(bundles).toContain(l.value)
      expect(l.native.trim()).not.toBe('')
    }
  })

  it('LANG_KEY matches the key i18n initialises from', () => {
    // 常量被导出正是为了让读写两侧不可能再各写各的。
    expect(LANG_KEY).toBe('sliverreshine-lang')
  })
})
