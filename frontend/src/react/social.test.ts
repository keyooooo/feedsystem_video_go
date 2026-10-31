import {it,expect} from 'vitest'
import {contactsFor,shouldSend} from './SocialPages'
it('deduplicates contacts and excludes the current user',()=>expect(contactsFor([{id:2,username:'a'},{id:1,username:'me'}],[{id:2,username:'a'}],1)).toEqual([{id:2,username:'a'}]))
it('does not send messages while composing Chinese or inserting a line break',()=>{expect(shouldSend({key:'Enter',shiftKey:false,nativeEvent:{isComposing:true}} as never)).toBe(false);expect(shouldSend({key:'Enter',shiftKey:true,nativeEvent:{isComposing:false}} as never)).toBe(false);expect(shouldSend({key:'Enter',shiftKey:false,nativeEvent:{isComposing:false}} as never)).toBe(true)})
