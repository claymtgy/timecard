<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { api, type Client, type Project, type TimeEntry } from '../api'
import EntryModal from '../components/EntryModal.vue'
import { addDays, clock, dayLabel, hours, money, today, weekStart, weekday } from '../format'
import { session } from '../session'
import { downloadCsv } from '../util'

// Every entry across workers, filterable — for checking hours before invoicing or payroll.
const filters = ref({
  worker_id: '' as number | '',
  project_id: '' as number | '',
  client_id: '' as number | '',
  from: weekStart(today()),
  to: addDays(weekStart(today()), 6),
  unbilled: false,
  unpaid: false,
})
const projects = ref<Project[]>([])
const clients = ref<Client[]>([])
const entries = ref<TimeEntry[]>([])
const editing = ref<TimeEntry | null>(null)
const error = ref('')

onMounted(async () => {
  ;[projects.value, clients.value] = await Promise.all([api.get<Project[]>('/api/projects'), api.get<Client[]>('/api/clients')])
})

const clientProjects = computed(() => projects.value.filter((p) => !filters.value.client_id || p.client_id === filters.value.client_id))

async function load() {
  error.value = ''
  try {
    entries.value = await api.get<TimeEntry[]>('/api/time-entries', { ...filters.value })
  } catch (e) {
    error.value = (e as Error).message
  }
}
watch(filters, load, { deep: true, immediate: true })

const byWorker = computed(() => {
  const m = new Map<string, { minutes: number; pay: number }>()
  for (const e of entries.value) {
    const t = m.get(e.worker_name) ?? { minutes: 0, pay: 0 }
    t.minutes += e.minutes
    t.pay += Math.round((e.minutes * e.pay_rate_cents) / 60)
    m.set(e.worker_name, t)
  }
  return [...m.entries()]
})
const total = computed(() => entries.value.reduce((s, e) => s + e.minutes, 0))

function exportCsv() {
  downloadCsv(
    `time-${filters.value.from}-to-${filters.value.to}.csv`,
    ['Date', 'Day', 'Worker', 'Client', 'Project', 'Task', 'Start', 'End', 'Break (min)', 'Hours', 'Billable', 'Invoice', 'Notes'],
    entries.value.map((e) => [
      e.work_date, weekday(e.work_date), e.worker_name, e.client_name, e.project_name, e.task_title, clock(e.start_minute),
      clock(e.end_minute), e.start_minute != null ? e.break_minutes : '', hours(e.minutes), e.billable ? 'yes' : 'no',
      e.invoice_number, e.notes,
    ]),
  )
}
</script>

<template>
  <div class="page-head">
    <h1>Time log</h1>
    <span class="spacer"></span>
    <button :disabled="!entries.length" @click="exportCsv">Export CSV</button>
  </div>
  <div class="filters">
    <label class="field">From<input v-model="filters.from" type="date" /></label>
    <label class="field">To<input v-model="filters.to" type="date" /></label>
    <label class="field">
      Worker
      <select v-model="filters.worker_id">
        <option value="">All</option>
        <option v-for="w in session.workers" :key="w.id" :value="w.id">{{ w.name }}</option>
      </select>
    </label>
    <label class="field">
      Project
      <select v-model="filters.project_id">
        <option value="">All</option>
        <option v-for="p in clientProjects" :key="p.id" :value="p.id">{{ p.name }}</option>
      </select>
    </label>
    <label class="field">
      Client
      <select v-model="filters.client_id" @change="filters.project_id = ''">
        <option value="">All</option>
        <option v-for="c in clients" :key="c.id" :value="c.id">{{ c.name }}</option>
      </select>
    </label>
    <label class="check"><input v-model="filters.unbilled" type="checkbox" /> Unbilled hourly only</label>
    <label class="check"><input v-model="filters.unpaid" type="checkbox" /> Unpaid only</label>
  </div>
  <div v-if="error" class="error">{{ error }}</div>

  <div class="stats">
    <div class="stat"><div class="k">Total hours</div><div class="v">{{ hours(total) }}</div></div>
    <div v-for="[name, t] in byWorker" :key="name" class="stat">
      <div class="k">{{ name }}</div>
      <div class="v">{{ hours(t.minutes) }} h</div>
      <div class="small muted">{{ money(t.pay) }} wages</div>
    </div>
  </div>

  <div class="card flush">
    <div class="table-wrap">
      <table>
        <thead>
          <tr><th>Date</th><th>Worker</th><th>Project</th><th>Task</th><th>Time</th><th class="num">Hours</th><th>Status</th><th>Notes</th></tr>
        </thead>
        <tbody>
          <tr v-for="e in entries" :key="e.id" class="clickable" @click="editing = e">
            <td class="nowrap">{{ dayLabel(e.work_date) }}</td>
            <td>{{ e.worker_name }}</td>
            <td>{{ e.project_name }}<div class="small muted">{{ e.client_name ?? '' }}</div></td>
            <td>{{ e.task_title ?? '' }}</td>
            <td class="nowrap small">{{ e.start_minute != null ? `${clock(e.start_minute)} – ${clock(e.end_minute)}` : 'manual' }}</td>
            <td class="num">{{ hours(e.minutes) }}</td>
            <td class="nowrap">
              <span v-if="e.billing_type === 'fixed'" class="badge fixed">bid</span>
              <span v-else-if="!e.billable" class="badge">no bill</span>
              <span v-else-if="e.invoice_number" class="badge sent">Inv #{{ e.invoice_number }}</span>
              <span v-else class="badge warn">unbilled</span>
              <span v-if="e.pay_run_id" class="badge" :class="e.pay_run_status === 'paid' ? 'paid' : 'draft'">{{ e.pay_run_status === 'paid' ? 'paid' : 'pay run' }}</span>
            </td>
            <td class="small">{{ e.notes }}</td>
          </tr>
        </tbody>
      </table>
    </div>
    <div v-if="!entries.length" class="empty">No entries match these filters.</div>
  </div>

  <EntryModal v-if="editing" :entry="editing" @close="editing = null" @saved="editing = null; load()" @deleted="editing = null; load()" />
</template>
