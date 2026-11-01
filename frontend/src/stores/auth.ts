import { defineStore } from "pinia";
import { computed, ref, onScopeDispose } from "vue";
import { authSession } from "../session/auth";
import { decodeJwtPayload } from "../utils/jwt";
export const useAuthStore = defineStore("auth", () => {
  const token = ref(authSession.getState().token),
    refreshToken = ref(authSession.getState().refreshToken);
  const unsubscribe = authSession.subscribe((state) => {
    token.value = state.token;
    refreshToken.value = state.refreshToken;
  });
  onScopeDispose(unsubscribe);
  const isLoggedIn = computed(() => !!token.value),
    claims = computed(() => decodeJwtPayload(token.value || ""));
  return {
    token,
    refreshToken,
    isLoggedIn,
    claims,
    ...{
      setToken: authSession.getState().setToken,
      setTokens: authSession.getState().setTokens,
      clearTokens: authSession.getState().clearTokens,
    },
  };
});
