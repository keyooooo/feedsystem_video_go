import {createStore} from 'zustand/vanilla'
import {decodeJwtPayload} from '../utils/jwt'
function read(key:string){try{return localStorage.getItem(key)}catch{return null}}
function persist(key:string,value:string|null){try{if(value)localStorage.setItem(key,value);else localStorage.removeItem(key)}catch{/* storage may be unavailable */}}
export const authSession=createStore<{
 token:string|null;refreshToken:string|null;revision:number;
 setToken:(token:string)=>void;setTokens:(token:string,refresh:string)=>void;clearTokens:()=>void;
}>()((set)=>({token:read('access_token'),refreshToken:read('refresh_token'),revision:0,
 setToken:token=>{persist('access_token',token);set({token})},
 setTokens:(token,refreshToken)=>{persist('access_token',token);persist('refresh_token',refreshToken);set(s=>({token,refreshToken,revision:s.revision+1}))},
 clearTokens:()=>{persist('access_token',null);persist('refresh_token',null);set(s=>({token:null,refreshToken:null,revision:s.revision+1}))}
}))
export const sessionClaims=()=>decodeJwtPayload(authSession.getState().token||'')
