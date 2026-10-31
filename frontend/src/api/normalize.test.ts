import {it,expect} from 'vitest'
import {listOrEmpty, normalizeFeedVideoList} from './normalize'
it('accepts nullable backend lists',()=>{expect(listOrEmpty(null)).toEqual([]);expect(normalizeFeedVideoList(undefined)).toEqual([])})
it('normalizes missing author and optional display fields',()=>{const [item]=normalizeFeedVideoList([{id:1} as never]);expect(item?.author.username).toBe('匿名用户');expect(item?.likes_count).toBe(0)})
