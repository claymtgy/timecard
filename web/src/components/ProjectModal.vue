<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api, type Client, type Project, type TaskTemplate } from '../api'
import { centsToDollars, dollarsToCents } from '../format'
import { activeWorkers } from '../session'
import { useAction } from '../util'
import Modal from './Modal.vue'

const props = defineProps<{ project?: Project | null; clientId?: number | null }>()
const emit = defineEmits<{ close: []; saved: [Project] }>()

const p = props.project
const form = ref({
  name: p?.name ?? '',
  client_id: p?.client_id ?? props.clientId ?? null,
  description: p?.description ?? '',
  billing_type: p?.billing_type ?? ('hourly' as 'hourly' | 'fixed'),
  bill_rate: centsToDollars(p?.bill_rate_cents ?? null),
  bid_amount: centsToDollars(p?.bid_amount_cents ?? null),
  labor_cost_rate: p ? centsToDollars(p.labor_cost_rate_cents ?? 0) : '20',
  pay_rate_override: centsToDollars(p?.pay_rate_override_cents ?? null),
  profit_share_pct: p?.profit_share_bps ? String(p.profit_share_bps / 100) : '',
  profit_share_worker_id: p?.profit_share_worker_id ?? null,
  status: p?.status ?? 'active',
  start_date: p?.start_date ?? '',
  completed_on: p?.completed_on ?? '',
  template_id: null as number | null,
})
const clients = ref<Client[]>([])
const templates = ref<TaskTemplate[]>([])
const { error, busy, run } = useAction()

onMounted(async () => {
  ;[clients.value, templates.value] = await Promise.all([
    api.get<Client[]>('/api/clients'),
    p ? Promise.resolve([]) : api.get<TaskTemplate[]>('/api/templates'),
  ])
})

async function save() {
  const f = form.value
  const body = {
    name: f.name,
    client_id: f.client_id,
    description: f.description,
    billing_type: f.billing_type,
    bill_rate_cents: f.billing_type === 'hourly' ? dollarsToCents(f.bill_rate) : 0,
    bid_amount_cents: f.billing_type === 'fixed' ? dollarsToCents(f.bid_amount) : 0,
    labor_cost_rate_cents: dollarsToCents(f.labor_cost_rate),
    pay_rate_override_cents: f.pay_rate_override === '' ? null : dollarsToCents(f.pay_rate_override),
    profit_share_bps: f.billing_type === 'fixed' ? Math.round(Number(f.profit_share_pct || 0) * 100) : 0,
    profit_share_worker_id: f.billing_type === 'fixed' && f.profit_share_pct ? f.profit_share_worker_id : null,
    status: f.status,
    start_date: f.start_date || null,
    completed_on: f.completed_on || null,
    template_id: p ? null : f.template_id,
  }
  const saved = await run(() => (p ? api.put<Project>(`/api/projects/${p.id}`, body) : api.post<Project>('/api/projects', body)))
  if (saved) emit('saved', saved)
}
</script>

<template>
  <Modal :title="p ? 'Edit project' : 'New project'" wide @close="emit('close')">
    <div v-if="error" class="error">{{ error }}</div>
    <form class="form-grid" @submit.prevent="save">
      <label class="field full">Name<input v-model="form.name" required placeholder="e.g. Smithfield Lot 117 roof" /></label>
      <label class="field">
        Client <span class="hint">who gets the invoice</span>
        <select v-model="form.client_id">
          <option :value="null">—</option>
          <option v-for="c in clients" :key="c.id" :value="c.id">{{ c.name }}</option>
        </select>
      </label>
      <label class="field">
        Status
        <select v-model="form.status">
          <option value="planned">Planned</option>
          <option value="active">Active (open for clock-in)</option>
          <option value="done">Done</option>
          <option value="cancelled">Cancelled</option>
        </select>
      </label>

      <div class="full seg" role="radiogroup" aria-label="Billing">
        <button type="button" :class="{ on: form.billing_type === 'hourly' }" @click="form.billing_type = 'hourly'">
          <strong>Hourly</strong><span class="small">Bill the client per hour worked</span>
        </button>
        <button type="button" :class="{ on: form.billing_type === 'fixed' }" @click="form.billing_type = 'fixed'">
          <strong>By bid</strong><span class="small">Fixed price; track profit vs. hours</span>
        </button>
      </div>

      <label v-if="form.billing_type === 'hourly'" class="field">
        Bill rate ($/h)<input v-model="form.bill_rate" inputmode="decimal" required placeholder="35" />
      </label>
      <label v-else class="field">
        Bid amount ($)<input v-model="form.bid_amount" inputmode="decimal" required placeholder="4170" />
      </label>
      <label class="field">
        Labor cost rate ($/h) <span class="hint">for profit</span>
        <input v-model="form.labor_cost_rate" inputmode="decimal" placeholder="20" />
      </label>
      <label class="field">
        Pay rate override ($/h) <span class="hint">blank = each worker's rate</span>
        <input v-model="form.pay_rate_override" inputmode="decimal" placeholder="—" />
      </label>
      <template v-if="form.billing_type === 'fixed'">
        <label class="field">Profit share (%)<input v-model="form.profit_share_pct" inputmode="decimal" placeholder="20" /></label>
        <label class="field">
          Share goes to
          <select v-model="form.profit_share_worker_id">
            <option :value="null">—</option>
            <option v-for="w in activeWorkers" :key="w.id" :value="w.id">{{ w.name }}</option>
          </select>
        </label>
      </template>
      <label class="field">Start date<input v-model="form.start_date" type="date" /></label>
      <label v-if="p" class="field">Completed on<input v-model="form.completed_on" type="date" /></label>
      <label v-if="!p" class="field">
        Start tasks from template
        <select v-model="form.template_id">
          <option :value="null">— none —</option>
          <option v-for="t in templates" :key="t.id" :value="t.id">{{ t.name }} ({{ t.items.length }} tasks)</option>
        </select>
      </label>
      <label class="field full">Description<textarea v-model="form.description" rows="2"></textarea></label>
      <button type="submit" hidden></button>
    </form>
    <template #foot>
      <button @click="emit('close')">Cancel</button>
      <button class="primary" :disabled="busy" @click="save">{{ p ? 'Save' : 'Create project' }}</button>
    </template>
  </Modal>
</template>

<style scoped>
.seg { display: grid; grid-template-columns: 1fr 1fr; gap: 10px; }
.seg button { flex-direction: column; align-items: flex-start; gap: 2px; padding: 12px; border-width: 2px; white-space: normal; text-align: left; }
.seg button.on { border-color: var(--accent); background: var(--accent-soft); }
.seg .small { font-weight: 400; color: var(--muted); }
</style>
