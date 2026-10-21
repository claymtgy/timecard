<script setup lang="ts">
import { ref } from 'vue'
import { api, type Worker } from '../api'
import Modal from '../components/Modal.vue'
import { centsToDollars, dollarsToCents, money } from '../format'
import { refreshWorkers, session } from '../session'
import { confirmDelete, useAction } from '../util'

const form = ref<{ id?: number; name: string; email: string; phone: string; pay_rate: string; active: boolean; notes: string } | null>(null)
const { error, busy, run } = useAction()

function edit(w?: Worker) {
  form.value = w
    ? { id: w.id, name: w.name, email: w.email, phone: w.phone, pay_rate: centsToDollars(w.pay_rate_cents), active: w.active, notes: w.notes }
    : { name: '', email: '', phone: '', pay_rate: '', active: true, notes: '' }
}
async function save() {
  const f = form.value!
  const body = { name: f.name, email: f.email, phone: f.phone, pay_rate_cents: dollarsToCents(f.pay_rate), active: f.active, notes: f.notes }
  const saved = await run(() => (f.id ? api.put<Worker>(`/api/workers/${f.id}`, body) : api.post<Worker>('/api/workers', body)))
  if (saved) {
    form.value = null
    await refreshWorkers()
  }
}
async function remove() {
  const f = form.value
  if (!f?.id || !confirmDelete(f.name)) return
  if (await run(() => api.del(`/api/workers/${f.id}`).then(() => true))) {
    form.value = null
    await refreshWorkers()
  }
}
</script>

<template>
  <div class="page-head">
    <h1>Workers</h1>
    <span class="spacer"></span>
    <button class="primary" @click="edit()">+ New worker</button>
  </div>
  <div class="card flush">
    <table>
      <thead><tr><th>Name</th><th class="num">Pay rate</th><th>Phone</th><th>Email</th><th>Sign-in</th><th>Status</th></tr></thead>
      <tbody>
        <tr v-for="w in session.workers" :key="w.id" class="clickable" @click="edit(w)">
          <td><strong>{{ w.name }}</strong></td>
          <td class="num">{{ w.pay_rate_cents ? `${money(w.pay_rate_cents)}/h` : '—' }}</td>
          <td class="nowrap">{{ w.phone }}</td>
          <td>{{ w.email }}</td>
          <td class="small">{{ w.user_email ?? '' }}</td>
          <td><span class="badge" :class="w.active ? 'active' : ''">{{ w.active ? 'Active' : 'Inactive' }}</span></td>
        </tr>
      </tbody>
    </table>
    <div v-if="!session.workers.length" class="empty">Add everyone who logs hours — employees, owners and subs.</div>
  </div>
  <p class="muted small">
    A worker with a $0 rate (e.g. an owner) still logs hours for invoicing and job costing but isn't paid wages.
    When someone signs in with the same email as a worker, their login is linked automatically.
  </p>

  <Modal v-if="form" :title="form.id ? 'Edit worker' : 'New worker'" @close="form = null">
    <div v-if="error" class="error">{{ error }}</div>
    <form class="form-grid" @submit.prevent="save">
      <label class="field full">Name<input v-model="form.name" required /></label>
      <label class="field">Pay rate ($/h)<input v-model="form.pay_rate" inputmode="decimal" placeholder="0" /></label>
      <label class="field">Phone<input v-model="form.phone" type="tel" /></label>
      <label class="field full">Email <span class="hint">used to link their sign-in</span><input v-model="form.email" type="email" /></label>
      <label class="field full">Notes<textarea v-model="form.notes"></textarea></label>
      <label class="check"><input v-model="form.active" type="checkbox" /> Active</label>
      <button type="submit" hidden></button>
    </form>
    <template #foot>
      <button v-if="form.id" class="danger" @click="remove">Delete</button>
      <span class="grow"></span>
      <button @click="form = null">Cancel</button>
      <button class="primary" :disabled="busy" @click="save">Save</button>
    </template>
  </Modal>
</template>
