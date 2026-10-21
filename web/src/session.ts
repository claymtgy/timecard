import { computed, reactive } from 'vue'
import { api, ApiError, setCsrfToken, setUnauthorizedHandler, type Me, type Worker } from './api'

// Signed-in user plus the worker list, which nearly every screen needs.
export const session = reactive({
  me: null as Me['user'] | null,
  workers: [] as Worker[],
  loaded: false,
  anonymous: false, // not signed in (or session expired): show the login screen
  error: '',
})

setUnauthorizedHandler(() => {
  session.anonymous = true
})

export const isManager = computed(() => session.me?.role === 'admin' || session.me?.role === 'manager')
export const isAdmin = computed(() => session.me?.role === 'admin')
export const activeWorkers = computed(() => session.workers.filter((w) => w.active))

let loading: Promise<void> | null = null

/** Loads /api/me once. Anonymous visitors get session.anonymous = true. */
export function ensureSession(): Promise<void> {
  loading ??= (async () => {
    try {
      const me = await api.get<Me>('/api/me')
      setCsrfToken(me.csrf_token)
      session.me = me.user
      await refreshWorkers()
      session.loaded = true
    } catch (e) {
      if (!(e instanceof ApiError && e.status === 401)) session.error = (e as Error).message
    }
  })()
  return loading
}

export async function refreshWorkers() {
  session.workers = await api.get<Worker[]>('/api/workers')
}

export function workerName(id: number | null | undefined): string {
  return session.workers.find((w) => w.id === id)?.name ?? ''
}

export async function logout() {
  const res = await api.post<{ redirect: string }>('/auth/logout')
  location.href = res.redirect || '/'
}
