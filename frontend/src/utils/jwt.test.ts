import { describe, it, expect } from 'vitest'
import { decodeJwtPayload } from './jwt'
describe('JWT display claims', () => {
 it('rejects malformed claims', () => { expect(decodeJwtPayload('broken')).toBeNull(); expect(decodeJwtPayload('a.!.c')).toBeNull() })
 it('decodes unicode claims', () => { const payload = btoa(unescape(encodeURIComponent(JSON.stringify({account_id: 7, username: '测试用户'})))); expect(decodeJwtPayload(`a.${payload}.c`)).toMatchObject({account_id:7,username:'测试用户'}) })
})
