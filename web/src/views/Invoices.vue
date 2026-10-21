<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { api, type Client, type Invoice, type InvoiceDetail, type UnbilledProject } from '../api'
import Modal from '../components/Modal.vue'
import { centsToDollars, dollarsToCents, hours, label, money, rangeLabel, today } from '../format'
import { downloadCsv, useAction } from '../util'

// The "Overview" sheet: every invoice with hours per worker, man-hours, total and status.
const router = useRouter()
const invoices = ref<Invoice[]>([])
const statusFilter = ref('')
const creating = ref(false)

async function load() {
  invoices.value = await api.get<Invoice[]>('/api/invoices', { status: statusFilter.value })
}
onMounted(load)
watch(statusFilter, load)

const workerCols = computed(() => {
  const m = new Map<number, string>()
  for (const i of invoices.value) for (const w of i.workers) m.set(w.worker_id, w.worker_name)
  return [...m.entries()].map(([id, name]) => ({ id, name }))
})
const wmins = (i: Invoice, id: number) => i.workers.find((w) => w.worker_id === id)?.minutes ?? 0
const outstanding = computed(() => invoices.value.filter((i) => i.status === 'sent').reduce((s, i) => s + i.total_cents, 0))

function exportCsv() {
  downloadCsv(
    'invoices.csv',
    ['Invoice #', 'Dates', 'Client', 'Projects', ...workerCols.value.map((w) => `${w.name} hrs`), 'Man hours', 'Total', 'Status', 'Sent', 'Paid date'],
    invoices.value.map((i) => [
      i.number, rangeLabel(i.period_start, i.period_end), i.client_name, i.projects, ...workerCols.value.map((w) => hours(wmins(i, w.id))),
      hours(i.minutes), (i.total_cents / 100).toFixed(2), i.status, i.sent_at?.slice(0, 10), i.paid_date,
    ]),
  )
}

// ---- new invoice wizard (per client) ----
const clients = ref<Client[]>([])
const nextNumber = computed(() => Math.max(0, ...invoices.value.map((i) => i.number)) + 1)
const form = ref({ client_id: null as number | null, period_start: '', period_end: '', number: '', issue_date: today(), due_date: '', notes: '' })
const unbilled = ref<UnbilledProject[]>([])
const pickedHourly = ref<number[]>([])
const pickedFixed = ref<Record<number, { on: boolean; amount: string }>>({})
const { error, busy, run } = useAction()

async function openCreate() {
  creating.value = true
  if (!clients.value.length) clients.value = await api.get<Client[]>('/api/clients')
  form.value = { client_id: clients.value[0]?.id ?? null, period_start: '', period_end: '', number: String(nextNumber.value), issue_date: today(), due_date: '', notes: '' }
}
async function loadUnbilled() {
  if (!form.value.client_id) {
    unbilled.value = []
    return
  }
  unbilled.value = await api.get<UnbilledProject[]>('/api/billing/unbilled', {
    client_id: form.value.client_id, from: form.value.period_start, to: form.value.period_end,
  })
  pickedHourly.value = unbilled.value.filter((u) => u.billing_type === 'hourly').map((u) => u.project_id)
  pickedFixed.value = Object.fromEntries(
    unbilled.value.filter((u) => u.billing_type === 'fixed')
      .map((u) => [u.project_id, { on: u.status === 'done', amount: centsToDollars(u.bid_amount_cents - u.invoiced_cents) }]),
  )
}
watch(() => [form.value.client_id, form.value.period_start, form.value.period_end], () => creating.value && loadUnbilled())

const hourly = computed(() => unbilled.value.filter((u) => u.billing_type === 'hourly'))
const fixed = computed(() => unbilled.value.filter((u) => u.billing_type === 'fixed'))
const preview = computed(() => {
  let t = 0
  for (const u of hourly.value) if (pickedHourly.value.includes(u.project_id)) t += u.amount_cents
  for (const u of fixed.value) if (pickedFixed.value[u.project_id]?.on) t += dollarsToCents(pickedFixed.value[u.project_id].amount)
  return t
})

async function create() {
  const f = form.value
  const d = await run(() =>
    api.post<InvoiceDetail>('/api/invoices', {
      client_id: f.client_id,
      number: f.number ? Number(f.number) : null,
      period_start: f.period_start || null,
      period_end: f.period_end || null,
      issue_date: f.issue_date || null,
      due_date: f.due_date || null,
      notes: f.notes,
      hourly_project_ids: pickedHourly.value,
      fixed_projects: fixed.value.filter((u) => pickedFixed.value[u.project_id]?.on)
        .map((u) => ({ project_id: u.project_id, amount_cents: dollarsToCents(pickedFixed.value[u.project_id].amount) })),
    }),
  )
  if (d) router.push(`/invoices/${d.invoice.id}`)
}
</script>

<template>
  <div class="page-head">
    <h1>Invoices</h1>
    <span class="muted">{{ money(outstanding) }} outstanding</span>
    <span class="spacer"></span>
    <select v-model="statusFilter" aria-label="Status">
      <option value="">All statuses</option>
      <option value="draft">Draft</option>
      <option value="sent">Sent</option>
      <option value="paid">Paid</option>
      <option value="void">Void</option>
    </select>
    <button :disabled="!invoices.length" @click="exportCsv">Export CSV</button>
    <button class="primary" @click="openCreate">+ New invoice</button>
  </div>

  <div class="card flush">
    <div class="table-wrap">
      <table>
        <thead>
          <tr>
            <th>#</th><th>Dates</th><th>Client / projects</th>
            <th v-for="w in workerCols" :key="w.id" class="num">{{ w.name }} hrs</th>
            <th class="num">Man hours</th><th class="num">Total</th><th>Status</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="i in invoices" :key="i.id" class="clickable" @click="router.push(`/invoices/${i.id}`)">
            <td><strong>{{ i.number }}</strong></td>
            <td class="nowrap">{{ rangeLabel(i.period_start, i.period_end) || i.issue_date }}</td>
            <td>{{ i.client_name ?? '—' }}<div class="small muted">{{ i.projects }}</div></td>
            <td v-for="w in workerCols" :key="w.id" class="num">{{ wmins(i, w.id) ? hours(wmins(i, w.id)) : '–' }}</td>
            <td class="num">{{ hours(i.minutes) }}</td>
            <td class="num"><strong>{{ money(i.total_cents) }}</strong></td>
            <td><span class="badge" :class="i.status">{{ label(i.status) }}</span></td>
          </tr>
        </tbody>
      </table>
    </div>
    <div v-if="!invoices.length" class="empty">No invoices yet. Create one from a client's unbilled hours or a finished bid.</div>
  </div>

  <Modal v-if="creating" title="New invoice" wide @close="creating = false">
    <div v-if="error" class="error">{{ error }}</div>
    <div class="form-grid">
      <label class="field">
        Client
        <select v-model="form.client_id">
          <option v-for="c in clients" :key="c.id" :value="c.id">{{ c.name }}</option>
        </select>
      </label>
      <label class="field">Hours from<input v-model="form.period_start" type="date" /></label>
      <label class="field">Hours to<input v-model="form.period_end" type="date" /></label>
      <label class="field">Invoice #<input v-model="form.number" inputmode="numeric" /></label>
      <label class="field">Issue date<input v-model="form.issue_date" type="date" /></label>
      <label class="field">Due date <span class="hint">blank = company default</span><input v-model="form.due_date" type="date" /></label>
    </div>
    <p v-if="!clients.length" class="neg">Add a client first (Clients page) and assign projects to it.</p>

    <h3 style="margin-top: 18px">Hourly projects — unbilled hours</h3>
    <table v-if="hourly.length">
      <thead><tr><th></th><th>Project</th><th>Dates</th><th class="num">Hours</th><th class="num">Rate</th><th class="num">Amount</th></tr></thead>
      <tbody>
        <tr v-for="u in hourly" :key="u.project_id">
          <td><input v-model="pickedHourly" type="checkbox" :value="u.project_id" :aria-label="`Include ${u.project_name}`" /></td>
          <td>{{ u.project_name }}</td>
          <td class="small">{{ rangeLabel(u.first_date, u.last_date) }}</td>
          <td class="num">{{ hours(u.minutes) }}</td>
          <td class="num">{{ money(u.bill_rate_cents) }}</td>
          <td class="num">{{ money(u.amount_cents) }}</td>
        </tr>
      </tbody>
    </table>
    <p v-else class="muted">No unbilled hourly time for this client{{ form.period_start || form.period_end ? ' in that period' : '' }}.</p>

    <h3 style="margin-top: 18px">Bid projects</h3>
    <table v-if="fixed.length">
      <thead><tr><th></th><th>Project</th><th>Status</th><th class="num">Bid</th><th class="num">Invoiced</th><th class="num">Bill now ($)</th></tr></thead>
      <tbody>
        <tr v-for="u in fixed" :key="u.project_id">
          <td><input v-model="pickedFixed[u.project_id].on" type="checkbox" :aria-label="`Include ${u.project_name}`" /></td>
          <td>{{ u.project_name }}</td>
          <td><span class="badge" :class="u.status">{{ label(u.status) }}</span></td>
          <td class="num">{{ money(u.bid_amount_cents) }}</td>
          <td class="num">{{ money(u.invoiced_cents) }}</td>
          <td class="num"><input v-model="pickedFixed[u.project_id].amount" inputmode="decimal" style="width: 110px; text-align: right" /></td>
        </tr>
      </tbody>
    </table>
    <p v-else class="muted">No bid projects left to invoice for this client.</p>
    <p class="small muted">Bill part of a bid (progress billing) by lowering “Bill now”; the rest stays available for a later invoice.</p>

    <label class="field" style="margin-top: 14px">Notes (printed on the invoice)<textarea v-model="form.notes"></textarea></label>
    <template #foot>
      <strong class="grow">Total {{ money(preview) }}</strong>
      <button @click="creating = false">Cancel</button>
      <button class="primary" :disabled="busy || preview <= 0" @click="create">Create draft</button>
    </template>
  </Modal>
</template>
