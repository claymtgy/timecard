<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { api, type Client, type ProjectReport } from '../api'
import { hours, label, money, percent } from '../format'
import { downloadCsv } from '../util'

// The "Bid Jobs" sheet: per project hours by worker, bid (or billed), labor, profit, share and share-holder pay.
const router = useRouter()
const clients = ref<Client[]>([])
const filters = ref({ client_id: '' as number | '', billing_type: 'fixed', status: '' })
const rows = ref<ProjectReport[]>([])

onMounted(async () => {
  clients.value = await api.get<Client[]>('/api/clients')
})
async function load() {
  rows.value = await api.get<ProjectReport[]>('/api/reports/projects', filters.value)
}
watch(filters, load, { deep: true, immediate: true })

const workerCols = computed(() => {
  const m = new Map<number, string>()
  for (const r of rows.value) for (const w of r.workers) m.set(w.worker_id, w.worker_name)
  return [...m.entries()].map(([id, name]) => ({ id, name }))
})
const mins = (r: ProjectReport, id: number) => r.workers.find((w) => w.worker_id === id)?.minutes ?? 0

const groups = computed(() => {
  const g = new Map<string, ProjectReport[]>()
  for (const r of rows.value) {
    const k = r.client_name ?? 'No client'
    if (!g.has(k)) g.set(k, [])
    g.get(k)!.push(r)
  }
  return [...g.entries()]
})
const sum = (list: ProjectReport[], f: (r: ProjectReport) => number) => list.reduce((s, r) => s + f(r), 0)

function exportCsv() {
  downloadCsv(
    'project-profit.csv',
    ['Client', 'Project', 'Billing', 'Status', ...workerCols.value.map((w) => `${w.name} hrs`), 'Total hrs', 'Revenue/Bid', 'Labor cost', 'Profit', 'Share %', 'Share', 'Share to', 'Share-holder pay', 'Invoiced', 'Paid'],
    rows.value.map((r) => [
      r.client_name, r.project_name, r.billing_type, r.status, ...workerCols.value.map((w) => hours(mins(r, w.id))), hours(r.minutes),
      (r.revenue_cents / 100).toFixed(2), (r.labor_cost_cents / 100).toFixed(2), (r.profit_cents / 100).toFixed(2),
      r.profit_share_bps / 100, (r.profit_share_cents / 100).toFixed(2), r.profit_share_worker_name ?? '',
      (r.share_worker_pay_cents / 100).toFixed(2), (r.invoiced_cents / 100).toFixed(2), (r.paid_cents / 100).toFixed(2),
    ]),
  )
}
</script>

<template>
  <div class="page-head">
    <h1>Project profit</h1>
    <span class="spacer"></span>
    <button :disabled="!rows.length" @click="exportCsv">Export CSV</button>
  </div>
  <div class="filters">
    <label class="field">
      Client
      <select v-model="filters.client_id">
        <option value="">All</option>
        <option v-for="c in clients" :key="c.id" :value="c.id">{{ c.name }}</option>
      </select>
    </label>
    <label class="field">
      Billing
      <select v-model="filters.billing_type">
        <option value="fixed">By bid</option>
        <option value="hourly">Hourly</option>
        <option value="">All</option>
      </select>
    </label>
    <label class="field">
      Status
      <select v-model="filters.status">
        <option value="">All</option>
        <option value="active">Active</option>
        <option value="done">Done</option>
        <option value="planned">Planned</option>
      </select>
    </label>
  </div>

  <div v-for="[client, list] in groups" :key="client" class="card flush">
    <div class="card-head"><h2>{{ client }}</h2></div>
    <div class="table-wrap">
      <table>
        <thead>
          <tr>
            <th>Project</th>
            <th v-for="w in workerCols" :key="w.id" class="num">{{ w.name }}</th>
            <th class="num">Hrs</th>
            <th class="num">{{ filters.billing_type === 'hourly' ? 'Billable' : 'Bid' }}</th>
            <th class="num">Labor</th>
            <th class="num">Profit</th>
            <th class="num">Share</th>
            <th class="num">Share-holder pay</th>
            <th class="num">Invoiced</th>
            <th>Status</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="r in list" :key="r.project_id" class="clickable" @click="router.push(`/projects/${r.project_id}`)">
            <td><strong>{{ r.project_name }}</strong></td>
            <td v-for="w in workerCols" :key="w.id" class="num">{{ mins(r, w.id) ? hours(mins(r, w.id)) : '' }}</td>
            <td class="num">{{ hours(r.minutes) }}</td>
            <td class="num">{{ money(r.revenue_cents) }}</td>
            <td class="num">{{ money(r.labor_cost_cents) }}</td>
            <td class="num" :class="r.profit_cents < 0 ? 'neg' : ''">{{ money(r.profit_cents) }}</td>
            <td class="num">
              <template v-if="r.profit_share_bps">{{ money(r.profit_share_cents) }}<div class="small muted">{{ percent(r.profit_share_bps) }} · {{ r.profit_share_worker_name }}</div></template>
            </td>
            <td class="num">{{ r.profit_share_worker_id ? money(r.share_worker_pay_cents) : '' }}</td>
            <td class="num">{{ money(r.invoiced_cents) }}<div v-if="r.paid_cents" class="small pos">{{ money(r.paid_cents) }} paid</div></td>
            <td><span class="badge" :class="r.status">{{ label(r.status) }}</span></td>
          </tr>
        </tbody>
        <tfoot>
          <tr>
            <td>Total</td>
            <td v-for="w in workerCols" :key="w.id" class="num">{{ hours(sum(list, (r) => mins(r, w.id))) }}</td>
            <td class="num">{{ hours(sum(list, (r) => r.minutes)) }}</td>
            <td class="num">{{ money(sum(list, (r) => r.revenue_cents)) }}</td>
            <td class="num">{{ money(sum(list, (r) => r.labor_cost_cents)) }}</td>
            <td class="num">{{ money(sum(list, (r) => r.profit_cents)) }}</td>
            <td class="num">{{ money(sum(list, (r) => r.profit_share_cents)) }}</td>
            <td class="num">{{ money(sum(list, (r) => (r.profit_share_worker_id ? r.share_worker_pay_cents : 0))) }}</td>
            <td class="num">{{ money(sum(list, (r) => r.invoiced_cents)) }}</td>
            <td></td>
          </tr>
        </tfoot>
      </table>
    </div>
  </div>
  <div v-if="!rows.length" class="card empty">No projects match.</div>
</template>
