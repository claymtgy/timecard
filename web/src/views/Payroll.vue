<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { api, type Adjustment, type PayRun, type PayRunDetail, type UnpaidWorker } from '../api'
import Modal from '../components/Modal.vue'
import { dateLabel, dollarsToCents, hours, label, money, rangeLabel, today } from '../format'
import { activeWorkers } from '../session'
import { useAction } from '../util'

// Replaces the per-person "Paid?" columns: what's owed, pay runs, and one-off pay items.
const router = useRouter()
const unpaid = ref<UnpaidWorker[]>([])
const runs = ref<PayRun[]>([])
const adjustments = ref<Adjustment[]>([])
const runForm = ref<{ worker_id: number; period_start: string; period_end: string; notes: string } | null>(null)
const adjForm = ref<{ worker_id: number; kind: string; description: string; amount: string; adj_date: string } | null>(null)
const { error, busy, run } = useAction()

async function load() {
  ;[unpaid.value, runs.value, adjustments.value] = await Promise.all([
    api.get<UnpaidWorker[]>('/api/payroll/unpaid'),
    api.get<PayRun[]>('/api/pay-runs'),
    api.get<Adjustment[]>('/api/adjustments', { unpaid: true }),
  ])
}
onMounted(load)

function newRun(u?: UnpaidWorker) {
  runForm.value = {
    worker_id: u?.worker_id ?? activeWorkers.value[0]?.id ?? 0,
    period_start: u?.first_date ?? today(),
    period_end: u?.last_date ?? today(),
    notes: '',
  }
}
async function createRun() {
  const d = await run(() => api.post<PayRunDetail>('/api/pay-runs', runForm.value))
  if (d) router.push(`/payroll/runs/${d.pay_run.id}`)
}
async function createAdj() {
  const f = adjForm.value!
  const a = await run(() => api.post<Adjustment>('/api/adjustments', {
    worker_id: f.worker_id, kind: f.kind, description: f.description, amount_cents: dollarsToCents(f.amount), adj_date: f.adj_date,
  }))
  if (a) {
    adjForm.value = null
    await load()
  }
}
async function deleteAdj(a: Adjustment) {
  if (!window.confirm(`Delete ${a.description || a.kind} (${money(a.amount_cents)})?`)) return
  if (await run(() => api.del(`/api/adjustments/${a.id}`).then(() => true))) await load()
}
const kindLabel: Record<string, string> = {
  profit_share: 'Profit share', bonus: 'Bonus', reimbursement: 'Reimbursement', deduction: 'Deduction', other: 'Other',
}
</script>

<template>
  <div class="page-head">
    <h1>Payroll</h1>
    <span class="spacer"></span>
    <button @click="adjForm = { worker_id: activeWorkers[0]?.id ?? 0, kind: 'bonus', description: '', amount: '', adj_date: today() }">+ Pay item</button>
    <button class="primary" @click="newRun()">+ Pay run</button>
  </div>
  <div v-if="error && !runForm && !adjForm" class="error">{{ error }}</div>

  <div class="card flush">
    <div class="card-head"><h2>Owed (not yet in a pay run)</h2></div>
    <table>
      <thead><tr><th>Worker</th><th>Dates</th><th class="num">Hours</th><th class="num">Wages</th><th class="num">Pay items</th><th class="num">Total</th><th></th></tr></thead>
      <tbody>
        <tr v-for="u in unpaid" :key="u.worker_id">
          <td><strong>{{ u.worker_name }}</strong><div class="small muted">{{ u.pay_rate_cents ? `${money(u.pay_rate_cents)}/h` : 'no hourly rate' }}</div></td>
          <td class="small">{{ rangeLabel(u.first_date, u.last_date) }}</td>
          <td class="num">{{ hours(u.minutes) }}</td>
          <td class="num">{{ money(u.wages_cents) }}</td>
          <td class="num">{{ money(u.adjustments_cents) }}</td>
          <td class="num"><strong>{{ money(u.total_cents) }}</strong></td>
          <td class="actions"><button class="small" @click="newRun(u)">Create pay run</button></td>
        </tr>
      </tbody>
    </table>
    <div v-if="!unpaid.length" class="empty">Everyone is paid up.</div>
  </div>

  <div class="card flush">
    <div class="card-head"><h2>Unpaid pay items</h2><span class="muted small">profit shares, bonuses, reimbursements, deductions</span></div>
    <table>
      <thead><tr><th>Date</th><th>Worker</th><th>Type</th><th>Description</th><th class="num">Amount</th><th></th></tr></thead>
      <tbody>
        <tr v-for="a in adjustments" :key="a.id">
          <td class="nowrap">{{ dateLabel(a.adj_date) }}</td>
          <td>{{ a.worker_name }}</td>
          <td>{{ kindLabel[a.kind] }}</td>
          <td>{{ a.description }}<RouterLink v-if="a.project_id" :to="`/projects/${a.project_id}`" class="small"> ({{ a.project_name }})</RouterLink></td>
          <td class="num" :class="a.amount_cents < 0 ? 'neg' : ''">{{ money(a.amount_cents) }}</td>
          <td class="actions"><button class="small ghost danger" @click="deleteAdj(a)">Delete</button></td>
        </tr>
      </tbody>
    </table>
    <div v-if="!adjustments.length" class="empty">None. Profit shares are added from a bid project's page.</div>
  </div>

  <div class="card flush">
    <div class="card-head"><h2>Pay runs</h2></div>
    <table>
      <thead><tr><th>Worker</th><th>Period</th><th class="num">Hours</th><th class="num">Total</th><th>Status</th><th>Paid</th></tr></thead>
      <tbody>
        <tr v-for="r in runs" :key="r.id" class="clickable" @click="router.push(`/payroll/runs/${r.id}`)">
          <td>{{ r.worker_name }}</td>
          <td class="nowrap">{{ rangeLabel(r.period_start, r.period_end) }}</td>
          <td class="num">{{ hours(r.minutes) }}</td>
          <td class="num">{{ money(r.total_cents) }}</td>
          <td><span class="badge" :class="r.status">{{ label(r.status) }}</span></td>
          <td class="small">{{ dateLabel(r.paid_date) }}</td>
        </tr>
      </tbody>
    </table>
    <div v-if="!runs.length" class="empty">No pay runs yet.</div>
  </div>

  <Modal v-if="runForm" title="New pay run" @close="runForm = null">
    <div v-if="error" class="error">{{ error }}</div>
    <p class="muted" style="margin-top: 0">Collects the worker's unpaid time in the period, plus unpaid pay items dated on or before the end.</p>
    <form class="form-grid" @submit.prevent="createRun">
      <label class="field full">
        Worker
        <select v-model.number="runForm.worker_id">
          <option v-for="w in activeWorkers" :key="w.id" :value="w.id">{{ w.name }}</option>
        </select>
      </label>
      <label class="field">From<input v-model="runForm.period_start" type="date" required /></label>
      <label class="field">To<input v-model="runForm.period_end" type="date" required /></label>
      <label class="field full">Notes<input v-model="runForm.notes" /></label>
      <button type="submit" hidden></button>
    </form>
    <template #foot>
      <button @click="runForm = null">Cancel</button>
      <button class="primary" :disabled="busy" @click="createRun">Create</button>
    </template>
  </Modal>

  <Modal v-if="adjForm" title="New pay item" @close="adjForm = null">
    <div v-if="error" class="error">{{ error }}</div>
    <form class="form-grid" @submit.prevent="createAdj">
      <label class="field">
        Worker
        <select v-model.number="adjForm.worker_id">
          <option v-for="w in activeWorkers" :key="w.id" :value="w.id">{{ w.name }}</option>
        </select>
      </label>
      <label class="field">
        Type
        <select v-model="adjForm.kind">
          <option value="bonus">Bonus</option>
          <option value="reimbursement">Reimbursement</option>
          <option value="deduction">Deduction</option>
          <option value="other">Other</option>
        </select>
      </label>
      <label class="field">Amount ($)<input v-model="adjForm.amount" inputmode="decimal" required /></label>
      <label class="field">Date<input v-model="adjForm.adj_date" type="date" required /></label>
      <label class="field full">Description<input v-model="adjForm.description" placeholder="e.g. Fence work flat rate" /></label>
      <button type="submit" hidden></button>
    </form>
    <template #foot>
      <button @click="adjForm = null">Cancel</button>
      <button class="primary" :disabled="busy" @click="createAdj">Add</button>
    </template>
  </Modal>
</template>
