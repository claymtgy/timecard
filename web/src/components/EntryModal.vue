<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { api, type Project, type Task, type TimeEntry, type TimeEntryInput } from '../api'
import { centsToDollars, clockInput, dollarsToCents, hours } from '../format'
import { activeWorkers, isManager, session } from '../session'
import { useAction } from '../util'
import Modal from './Modal.vue'

const props = defineProps<{
  entry?: TimeEntry | null
  defaults?: Partial<TimeEntryInput>
}>()
const emit = defineEmits<{ close: []; saved: [TimeEntry]; deleted: [number] }>()

const e = props.entry
const form = ref({
  worker_id: e?.worker_id ?? props.defaults?.worker_id ?? session.me?.worker_id ?? 0,
  project_id: e?.project_id ?? props.defaults?.project_id ?? 0,
  task_id: e?.task_id ?? props.defaults?.task_id ?? null,
  work_date: e?.work_date ?? props.defaults?.work_date ?? '',
  start: e ? clockInput(e.start_minute) : '07:00',
  end: e ? clockInput(e.end_minute) : '',
  break_minutes: e?.break_minutes ?? 30,
  duration: e && e.start_minute == null ? hours(e.minutes) : '',
  billable: e?.billable ?? true,
  notes: e?.notes ?? '',
  pay_rate: e ? centsToDollars(e.pay_rate_cents) : '',
})
const mode = ref<'clock' | 'duration'>(e && e.start_minute == null ? 'duration' : 'clock')
const projects = ref<Project[]>([])
const tasks = ref<Task[]>([])
const { error, busy, run } = useAction()

onMounted(async () => {
  const all = await api.get<Project[]>('/api/projects')
  projects.value = all.filter((p) => p.status === 'active' || p.status === 'planned' || p.id === form.value.project_id)
  if (!form.value.project_id && projects.value.length === 1) form.value.project_id = projects.value[0].id
  await loadTasks()
})

async function loadTasks() {
  tasks.value = form.value.project_id ? await api.get<Task[]>('/api/tasks', { project_id: form.value.project_id }) : []
  if (form.value.task_id && !tasks.value.some((t) => t.id === form.value.task_id)) form.value.task_id = null
}
watch(() => form.value.project_id, loadTasks)

const projectsByClient = computed(() => {
  const groups = new Map<string, Project[]>()
  for (const p of projects.value) {
    const k = p.client_name ?? 'No client'
    if (!groups.has(k)) groups.set(k, [])
    groups.get(k)!.push(p)
  }
  return [...groups.entries()]
})

function toMin(v: string): number | null {
  const m = /^(\d{1,2}):(\d{2})$/.exec(v)
  return m ? Number(m[1]) * 60 + Number(m[2]) : null
}

const preview = computed(() => {
  if (mode.value === 'duration') return Number(form.value.duration) > 0 ? `${form.value.duration} h` : ''
  const s = toMin(form.value.start)
  const en = toMin(form.value.end)
  if (s == null || en == null || en <= s) return ''
  const worked = en - s - (Number(form.value.break_minutes) || 0)
  return worked > 0 ? `${hours(worked)} h` : ''
})

async function save() {
  const f = form.value
  const body: TimeEntryInput = {
    worker_id: Number(f.worker_id),
    project_id: Number(f.project_id),
    task_id: f.task_id ? Number(f.task_id) : null,
    work_date: f.work_date,
    start: mode.value === 'clock' ? f.start : '',
    end: mode.value === 'clock' ? f.end : '',
    break_minutes: mode.value === 'clock' ? Number(f.break_minutes) || 0 : 0,
    minutes: mode.value === 'duration' ? Math.round(Number(f.duration) * 60) : null,
    billable: f.billable,
    notes: f.notes,
  }
  if (isManager.value && f.pay_rate !== '') body.pay_rate_cents = dollarsToCents(f.pay_rate)
  const saved = await run(() =>
    e ? api.put<TimeEntry>(`/api/time-entries/${e.id}`, body) : api.post<TimeEntry>('/api/time-entries', body),
  )
  if (saved) emit('saved', saved)
}

async function remove() {
  if (!e || !window.confirm('Delete this time entry?')) return
  const done = await run(() => api.del(`/api/time-entries/${e.id}`).then(() => true))
  if (done) emit('deleted', e.id)
}

const locked = computed(() => !!(e && (e.invoice_id || e.pay_run_id)))
</script>

<template>
  <Modal :title="e ? 'Edit time' : 'Log time'" @close="emit('close')">
    <div v-if="error" class="error">{{ error }}</div>
    <div v-if="locked" class="error">
      This entry is {{ e?.invoice_id ? `on invoice #${e?.invoice_number}` : 'in a pay run' }} and can't be changed.
    </div>
    <form class="form-grid" @submit.prevent="save">
      <label v-if="isManager" class="field">
        Worker
        <select v-model.number="form.worker_id" required>
          <option :value="0" disabled>Choose…</option>
          <option v-for="w in activeWorkers" :key="w.id" :value="w.id">{{ w.name }}</option>
        </select>
      </label>
      <label class="field">
        Date
        <input v-model="form.work_date" type="date" required />
      </label>
      <label class="field full">
        Project
        <select v-model.number="form.project_id" required>
          <option :value="0" disabled>Choose a project…</option>
          <optgroup v-for="[client, list] in projectsByClient" :key="client" :label="client">
            <option v-for="p in list" :key="p.id" :value="p.id">
              {{ p.name }}{{ p.billing_type === 'fixed' ? ' (bid)' : '' }}
            </option>
          </optgroup>
        </select>
      </label>
      <label v-if="tasks.length" class="field full">
        Task <span class="hint">optional</span>
        <select v-model="form.task_id">
          <option :value="null">—</option>
          <option v-for="t in tasks" :key="t.id" :value="t.id">{{ t.title }}</option>
        </select>
      </label>

      <div class="full row">
        <label class="check"><input v-model="mode" type="radio" value="clock" /> Start / end</label>
        <label class="check"><input v-model="mode" type="radio" value="duration" /> Hours only</label>
        <span class="grow"></span>
        <strong v-if="preview">{{ preview }}</strong>
      </div>
      <template v-if="mode === 'clock'">
        <label class="field">Start<input v-model="form.start" type="time" required /></label>
        <label class="field">End<input v-model="form.end" type="time" required /></label>
        <label class="field">Break (min)<input v-model.number="form.break_minutes" type="number" min="0" step="5" /></label>
      </template>
      <label v-else class="field">
        Hours
        <input v-model="form.duration" type="number" min="0.05" max="24" step="0.25" required />
      </label>

      <label class="field full">Notes<textarea v-model="form.notes" rows="2" placeholder="What was done, anything unusual"></textarea></label>
      <template v-if="isManager">
        <label class="check">
          <input v-model="form.billable" type="checkbox" /> Billable to client
        </label>
        <label class="field">
          Pay rate ($/h) <span class="hint">blank = default</span>
          <input v-model="form.pay_rate" inputmode="decimal" placeholder="default" />
        </label>
      </template>
      <button type="submit" hidden></button>
    </form>
    <template #foot>
      <button v-if="e && !locked" class="danger" :disabled="busy" @click="remove">Delete</button>
      <span class="grow"></span>
      <button @click="emit('close')">Cancel</button>
      <button class="primary" :disabled="busy || locked" @click="save">{{ e ? 'Save' : 'Add entry' }}</button>
    </template>
  </Modal>
</template>
