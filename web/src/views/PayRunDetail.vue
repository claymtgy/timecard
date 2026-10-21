<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { api, type PayRunDetail } from '../api'
import { clock, dateLabel, dayLabel, hours, label, money, rangeLabel, today } from '../format'
import { confirmDelete, useAction } from '../util'

const props = defineProps<{ id: string }>()
const router = useRouter()
const d = ref<PayRunDetail | null>(null)
const { error, busy, run } = useAction()

onMounted(async () => {
  d.value = await api.get<PayRunDetail>(`/api/pay-runs/${props.id}`)
})
const pr = computed(() => d.value?.pay_run)

async function markPaid() {
  const date = window.prompt('Paid on (YYYY-MM-DD)', today())
  if (!date) return
  const r = await run(() => api.patch<PayRunDetail>(`/api/pay-runs/${props.id}`, { status: 'paid', paid_date: date }))
  if (r) d.value = r
}
async function markDraft() {
  const r = await run(() => api.patch<PayRunDetail>(`/api/pay-runs/${props.id}`, { status: 'draft' }))
  if (r) d.value = r
}
async function remove() {
  if (!confirmDelete('this pay run (its time becomes unpaid again)')) return
  if (await run(() => api.del(`/api/pay-runs/${props.id}`).then(() => true))) router.push('/payroll')
}
function print() {
  window.print()
}
</script>

<template>
  <template v-if="d && pr">
    <div class="crumbs no-print"><RouterLink to="/payroll">Payroll</RouterLink></div>
    <div class="page-head">
      <h1>{{ pr.worker_name }} · {{ rangeLabel(pr.period_start, pr.period_end) }}</h1>
      <span class="badge" :class="pr.status">{{ label(pr.status) }}</span>
      <span class="spacer"></span>
      <button @click="print">Print</button>
      <template v-if="pr.status === 'draft'">
        <button class="danger" @click="remove">Delete</button>
        <button class="primary" :disabled="busy" @click="markPaid">Mark paid</button>
      </template>
      <button v-else @click="markDraft">Mark unpaid</button>
    </div>
    <div v-if="error" class="error">{{ error }}</div>

    <div class="stats">
      <div class="stat"><div class="k">Hours</div><div class="v">{{ hours(pr.minutes) }}</div></div>
      <div class="stat"><div class="k">Wages</div><div class="v">{{ money(pr.wages_cents) }}</div></div>
      <div class="stat"><div class="k">Pay items</div><div class="v">{{ money(pr.adjustments_cents) }}</div></div>
      <div class="stat"><div class="k">Total</div><div class="v">{{ money(pr.total_cents) }}</div><div v-if="pr.paid_date" class="small muted">Paid {{ dateLabel(pr.paid_date) }}</div></div>
    </div>

    <div class="card flush">
      <div class="card-head"><h2>Time</h2></div>
      <table>
        <thead><tr><th>Date</th><th>Project</th><th>Time</th><th class="num">Hours</th><th class="num">Rate</th><th class="num">Pay</th></tr></thead>
        <tbody>
          <tr v-for="e in d.entries" :key="e.id">
            <td class="nowrap">{{ dayLabel(e.work_date) }}</td>
            <td>{{ e.project_name }}<div class="small muted">{{ e.task_title ?? '' }}</div></td>
            <td class="small nowrap">{{ e.start_minute != null ? `${clock(e.start_minute)} – ${clock(e.end_minute)}` : '' }}</td>
            <td class="num">{{ hours(e.minutes) }}</td>
            <td class="num">{{ money(e.pay_rate_cents) }}</td>
            <td class="num">{{ money(Math.round((e.minutes * e.pay_rate_cents) / 60)) }}</td>
          </tr>
        </tbody>
      </table>
      <div v-if="!d.entries.length" class="empty">No time in this run.</div>
    </div>

    <div v-if="d.adjustments.length" class="card flush">
      <div class="card-head"><h2>Pay items</h2></div>
      <table>
        <tbody>
          <tr v-for="a in d.adjustments" :key="a.id">
            <td class="nowrap">{{ dateLabel(a.adj_date) }}</td>
            <td>{{ a.description || a.kind }}</td>
            <td class="num">{{ money(a.amount_cents) }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </template>
</template>
