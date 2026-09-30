import { describe, it, expect } from 'vitest'
import { joinPath, parentOf } from '../paths'

describe('joinPath', () => {
  it('joins with the POSIX separator', () => {
    expect(joinPath('/home/user', 'file.txt', '/')).toBe('/home/user/file.txt')
    expect(joinPath('/home/user/', 'file.txt', '/')).toBe('/home/user/file.txt')
    expect(joinPath('', 'file.txt', '/')).toBe('/file.txt')
  })

  it('joins with the Windows separator', () => {
    expect(joinPath('C:\\Users\\test', 'file.txt', '\\')).toBe('C:\\Users\\test\\file.txt')
    expect(joinPath('C:\\Users\\test\\', 'file.txt', '\\')).toBe('C:\\Users\\test\\file.txt')
  })
})

describe('parentOf', () => {
  it('walks up POSIX paths', () => {
    expect(parentOf('/home/user', '/')).toBe('/home')
    expect(parentOf('/home/user/', '/')).toBe('/home')
    expect(parentOf('/home', '/')).toBe('/')
    expect(parentOf('/', '/')).toBe('/')
    expect(parentOf('', '/')).toBe('')
  })

  it('walks up Windows paths', () => {
    expect(parentOf('C:\\Users\\test', '\\')).toBe('C:\\Users')
    expect(parentOf('C:\\Users\\test\\', '\\')).toBe('C:\\Users')
    expect(parentOf('C:\\', '\\')).toBe('/')
  })
})
