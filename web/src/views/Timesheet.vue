<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api, type TimeEntry } from '../api'
import EntryModal from '../components/EntryModal.vue'
import { addDays, clock, dayLabel, hours, money, rangeLabel, today, weekStart } from '../format'
import { activeWorkers, isManager, session } from '../session'

// Weekly sheet per worker — the equivalent of one person's tab in the spreadsheet.
const route = useRoute()
const router = useRouter()

const workerId = ref<number>(Number(route.query.worker) || session.me?.worker_id || activeWorkers.value[0]?.id || 0)
const start = ref(weekStart(String(route.query.week || today())))
const entries = ref<TimeEntry[]>([])
const loading = ref(false)
const editing = ref<TimeEntry | null>(null)
const adding = ref<string | null>(null) // date for a new entry

const days = computed(() => Array.from({ length: 7 }, (_, i) => addDays(start.value, i)))
const end = computed(() => days.value[6])

async function load() {
  if (!workerId.value) return
  loading.value = true
  try {
    entries.value = await api.get<TimeEntry[]>('/api/time-entries', { worker_id: workerId.value, from: start.value, to: end.value })
  } finally {
    loading.value = false
  }
  router.replace({ query: { worker: workerId.value, week: start.value } })
}
watch([workerId, start], load, { immediate: true })

const byDay = computed(() => {
  const m = new Map<string, TimeEntry[]>()
  for (const d of days.value) m.set(d, [])
  for (const e of entries.value) m.get(e.work_date)?.push(e)
  return m
})
const dayTotal = (d: string) => (byDay.value.get(d) ?? []).reduce((s, e) => s + e.minutes, 0)

const totals = computed(() => {
  let all = 0, hourly = 0, fixed = 0, pay = 0
  for (const e of entries.value) {
    all += e.minutes
    if (e.billing_type === 'hourly') hourly += e.minutes
    else fixed += e.minutes
    pay += Math.round((e.minutes * e.pay_rate_cents) / 60)
  }
  return { all, hourly, fixed, pay }
})

const unlinked = computed(() => !isManager.value && !session.me?.worker_id)

function shift(weeks: number) {
  start.value = addDays(start.value, weeks * 7)
}

function onSaved() {
  editing.value = null
  adding.value = null
  load()
}
</script>

<template>
  <div class="page-head">
    <h1>Timesheet</h1>
    <span class="spacer"></span>
    <select v-if="isManager" v-model.number="workerId" aria-label="Worker">
      <option v-for="w in activeWorkers" :key="w.id" :value="w.id">{{ w.name }}</option>
    </select>
    <div class="week-nav">
      <button aria-label="Previous week" @click="shift(-1)">‹</button>
      <span class="range">{{ rangeLabel(start, end) }}</span>
      <button aria-label="Next week" @click="shift(1)">›</button>
      <button @click="start = weekStart(today())">This week</button>
    </div>
    <button class="primary" :disabled="!workerId" @click="adding = today() >= start && today() <= end ? today() : start">+ Log time</button>
  </div>

  <div v-if="unlinked" class="card empty">
    Your sign-in isn't linked to a worker yet. Ask an admin to link your account on the Users page.
  </div>
  <div v-else-if="!workerId" class="card empty">Add a worker first (Workers page).</div>
  <template v-else>
    <div class="stats">
      <div class="stat"><div class="k">Week total</div><div class="v">{{ hours(totals.all) }} h</div></div>
      <div class="stat"><div class="k">Hourly projects</div><div class="v">{{ hours(totals.hourly) }} h</div></div>
      <div class="stat"><div class="k">Bid projects</div><div class="v">{{ hours(totals.fixed) }} h</div></div>
      <div class="stat"><div class="k">Wages at entry rates</div><div class="v">{{ money(totals.pay) }}</div></div>
    </div>

    <div class="card flush">
      <div class="table-wrap">
        <table>
          <thead>
            <tr>
              <th>Project</th><th>Task</th><th>Start</th><th>End</th><th class="num">Break</th><th class="num">Hours</th><th>Notes</th><th></th>
            </tr>
          </thead>
          <tbody>
            <template v-for="d in days" :key="d">
              <tr class="day-head">
                <td colspan="5">{{ dayLabel(d) }}</td>
                <td class="num">{{ dayTotal(d) ? hours(dayTotal(d)) : '' }}</td>
                <td></td>
                <td class="actions"><button class="small ghost" @click="adding = d">+ Add</button></td>
              </tr>
              <tr v-for="e in byDay.get(d)" :key="e.id" class="clickable" @click="editing = e">
                <td>
                  <div>{{ e.project_name }} <span v-if="e.billing_type === 'fixed'" class="badge fixed">bid</span></div>
                  <div class="small muted">{{ e.client_name ?? '' }}</div>
                </td>
                <td>{{ e.task_title ?? '' }}</td>
                <td class="nowrap">{{ clock(e.start_minute) }}</td>
                <td class="nowrap">{{ clock(e.end_minute) }}</td>
                <td class="num">{{ e.start_minute != null ? `${e.break_minutes}m` : '' }}</td>
                <td class="num"><strong>{{ hours(e.minutes) }}</strong></td>
                <td class="small">
                  {{ e.notes }}
                  <span v-if="!e.billable" class="badge">not billable</span>
                </td>
                <td class="actions">
                  <span v-if="e.invoice_number" class="badge sent">Inv #{{ e.invoice_number }}</span>
                  <span v-if="e.pay_run_id" class="badge" :class="e.pay_run_status === 'paid' ? 'paid' : 'draft'">
                    {{ e.pay_run_status === 'paid' ? 'Paid' : 'In pay run' }}
                  </span>
                </td>
              </tr>
            </template>
          </tbody>
          <tfoot>
            <tr><td colspan="5">Week total</td><td class="num">{{ hours(totals.all) }}</td><td colspan="2"></td></tr>
          </tfoot>
        </table>
      </div>
      <div v-if="loading" class="empty">Loading…</div>
    </div>
  </template>

  <EntryModal v-if="editing" :entry="editing" @close="editing = null" @saved="onSaved" @deleted="onSaved" />
  <EntryModal
    v-if="adding"
    :defaults="{ worker_id: workerId, work_date: adding }"
    @close="adding = null"
    @saved="onSaved"
  />
</template>
