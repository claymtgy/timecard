<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api, type AppUser, type Role } from '../api'
import { refreshWorkers, session } from '../session'
import { useAction } from '../util'

const users = ref<AppUser[]>([])
const { error, run } = useAction()

onMounted(async () => {
  users.value = await api.get<AppUser[]>('/api/users')
})

async function update(u: AppUser, body: { role?: Role; worker_id?: number; clear_worker?: boolean }) {
  const updated = await run(() => api.patch<AppUser>(`/api/users/${u.id}`, body))
  if (updated) {
    Object.assign(u, updated)
    await refreshWorkers()
  } else {
    users.value = await api.get<AppUser[]>('/api/users') // revert the select
  }
}
// Fetched only when asked for, so it isn't on screen by default.
const orgPassword = ref<{ password: string | null } | null>(null)
const copied = ref(false)
async function showOrgPassword() {
  const r = await run(() => api.get<{ password: string | null }>('/api/organization/password'))
  if (r) orgPassword.value = r
}
async function copyOrgPassword() {
  if (!orgPassword.value?.password) return
  await navigator.clipboard.writeText(orgPassword.value.password)
  copied.value = true
  setTimeout(() => (copied.value = false), 2000)
}
function onWorker(u: AppUser, v: string) {
  update(u, v === '' ? { clear_worker: true } : { worker_id: Number(v) })
}
</script>

<template>
  <div class="page-head"><h1>Users</h1></div>
  <p class="muted" style="margin-top: -8px">
    People appear here after their first sign-in. <strong>Admins</strong> manage everything including users,
    <strong>managers</strong> see money (invoices, payroll, profit), <strong>workers</strong> log their own time and work their tasks.
  </p>
  <div v-if="error" class="error">{{ error }}</div>
  <div class="card" style="margin-bottom: 16px">
    <h2>Organization password</h2>
    <p class="small muted" style="margin-top: 0">New people enter this when they choose <strong>Create one</strong> on the sign-in page. Anyone who has it can join {{ session.me?.organization }}, so share it only with your crew.</p>
    <div v-if="orgPassword?.password" class="row">
      <code style="font-size: 16px">{{ orgPassword.password }}</code>
      <button class="small" @click="copyOrgPassword">{{ copied ? 'Copied' : 'Copy' }}</button>
      <button class="small ghost" @click="orgPassword = null">Hide</button>
    </div>
    <p v-else-if="orgPassword" class="neg" style="margin-bottom: 0">This password can't be displayed because it was set before this feature existed. Ask whoever runs the service to run <code>idp org passwd</code>, which replaces it.</p>
    <button v-else class="small" @click="showOrgPassword">Show password</button>
  </div>
  <div class="card flush">
    <table>
      <thead><tr><th>Name</th><th>Email</th><th>Role</th><th>Worker (for timesheets)</th><th>Last sign-in</th></tr></thead>
      <tbody>
        <tr v-for="u in users" :key="u.id">
          <td><strong>{{ u.name }}</strong></td>
          <td>{{ u.email }}</td>
          <td>
            <select :value="u.role" :aria-label="`Role for ${u.name}`" @change="update(u, { role: ($event.target as HTMLSelectElement).value as Role })">
              <option value="admin">Admin</option>
              <option value="manager">Manager</option>
              <option value="worker">Worker</option>
            </select>
          </td>
          <td>
            <select :value="u.worker_id ?? ''" :aria-label="`Worker for ${u.name}`" @change="onWorker(u, ($event.target as HTMLSelectElement).value)">
              <option value="">— not linked —</option>
              <option v-for="w in session.workers" :key="w.id" :value="w.id">{{ w.name }}</option>
            </select>
          </td>
          <td class="small">{{ u.last_login_at ? new Date(u.last_login_at).toLocaleString() : '' }}</td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
