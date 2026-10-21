<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { api, type ClockSession, type ClockState, type Project, type Task, type TimeEntry } from '../api'
import { clock, hours } from '../format'
import { isManager, session } from '../session'
import { useAction } from '../util'

// Clock in: pick a project, then a task — both preselected from the previous clock-in —
// then one tap. While on the clock: live timer, switch task, clock out.
const state = ref<ClockState | null>(null)
const projects = ref<Project[]>([])
const tasks = ref<Task[]>([])
const step = ref<'project' | 'task' | 'running' | 'out'>('project')
const switching = ref(false) // choosing a new project/task while on the clock
const projectId = ref<number | null>(null)
const taskId = ref<number | null>(null)
const notes = ref('')
const search = ref('')
const breakMinutes = ref(0)
const outNotes = ref('')
const logged = ref<TimeEntry[] | null>(null)
const now = ref(Date.now())
let skew = 0
let timer: number | undefined
const { error, busy, run } = useAction()

const sess = computed<ClockSession | null>(() => state.value?.session ?? null)

async function load() {
  const [st, ps] = await Promise.all([api.get<ClockState>('/api/clock'), api.get<Project[]>('/api/projects', { open: true })])
  state.value = st
  projects.value = ps
  skew = Date.parse(st.server_time) - Date.now()
  if (st.session) {
    step.value = 'running'
  } else {
    step.value = 'project'
    // Autofill from the previous clock-in (if that project is still open).
    const last = st.last
    projectId.value = last && ps.some((p) => p.id === last.project_id) ? last.project_id : null
    taskId.value = last && projectId.value === last.project_id ? last.task_id : null
  }
  breakMinutes.value = st.last?.break_minutes ?? 0
}

onMounted(async () => {
  await run(load)
  timer = window.setInterval(() => (now.value = Date.now()), 1000)
})
onBeforeUnmount(() => clearInterval(timer))

const filteredProjects = computed(() => {
  const q = search.value.trim().toLowerCase()
  const list = q ? projects.value.filter((p) => `${p.name} ${p.client_name ?? ''}`.toLowerCase().includes(q)) : projects.value
  // Selected (last used) project first.
  return [...list].sort((a, b) => Number(b.id === projectId.value) - Number(a.id === projectId.value))
})
const selectedProject = computed(() => projects.value.find((p) => p.id === projectId.value) ?? null)
const selectedTask = computed(() => tasks.value.find((t) => t.id === taskId.value) ?? null)

async function toTasks() {
  if (!projectId.value) return
  const list = await run(() => api.get<Task[]>('/api/tasks', { project_id: projectId.value }))
  if (!list) return
  tasks.value = list.filter((t) => t.status !== 'done' || t.id === taskId.value)
  if (taskId.value && !tasks.value.some((t) => t.id === taskId.value)) taskId.value = null
  // Only one open task: preselect it.
  if (!taskId.value && tasks.value.length === 1) taskId.value = tasks.value[0].id
  step.value = 'task'
}

function pickProject(id: number) {
  if (projectId.value !== id) taskId.value = state.value?.last?.project_id === id ? state.value.last.task_id : null
  projectId.value = id
  toTasks()
}

async function clockIn() {
  const path = switching.value ? '/api/clock/switch' : '/api/clock/in'
  const body = { project_id: projectId.value, task_id: taskId.value, notes: notes.value, break_minutes: switching.value ? breakMinutes.value : 0 }
  const res = await run(() => api.post<{ session: ClockSession; entries: TimeEntry[] }>(path, body))
  if (!res) return
  if (switching.value) logged.value = res.entries
  switching.value = false
  notes.value = ''
  await load()
}

async function clockOut() {
  const res = await run(() => api.post<{ entries: TimeEntry[] }>('/api/clock/out', { break_minutes: breakMinutes.value, notes: outNotes.value }))
  if (!res) return
  logged.value = res.entries
  outNotes.value = ''
  await load()
}

async function cancelClock() {
  if (!window.confirm('Discard this clock-in without recording any time?')) return
  if (await run(() => api.del('/api/clock').then(() => true))) await load()
}

function startSwitch() {
  switching.value = true
  projectId.value = sess.value?.project_id ?? null
  taskId.value = sess.value?.task_id ?? null
  step.value = 'project'
}

const elapsedMs = computed(() => (sess.value ? Math.max(0, now.value + skew - Date.parse(sess.value.started_at)) : 0))
const elapsed = computed(() => {
  const s = Math.floor(elapsedMs.value / 1000)
  return `${Math.floor(s / 3600)}:${String(Math.floor((s % 3600) / 60)).padStart(2, '0')}:${String(s % 60).padStart(2, '0')}`
})
const startedLabel = computed(() => {
  if (!sess.value) return ''
  const d = new Date(sess.value.started_at)
  return clock(d.getHours() * 60 + d.getMinutes())
})
const loggedMinutes = computed(() => (logged.value ?? []).reduce((s, e) => s + e.minutes, 0))
</script>

<template>
  <div class="clock">
    <div v-if="error" class="error">{{ error }}</div>

    <div v-if="state && !state.linked" class="card empty">
      <h2>Almost ready</h2>
      Your sign-in isn't linked to a worker yet. Ask an admin to link your account on the Users page.
    </div>

    <template v-else-if="state">
      <div v-if="logged && logged.length" class="card done-banner">
        ✅ Logged <strong>{{ hours(loggedMinutes) }} h</strong> to {{ logged[0].project_name }}{{ logged[0].task_title ? ` · ${logged[0].task_title}` : '' }}
        <button class="small ghost" @click="logged = null">✕</button>
      </div>

      <!-- On the clock -->
      <div v-if="step === 'running' && sess" class="card running">
        <div class="small muted">On the clock since {{ startedLabel }}</div>
        <div class="timer">{{ elapsed }}</div>
        <div class="now-project">{{ sess.project_name }}</div>
        <div class="muted">{{ sess.task_title ?? 'No specific task' }}<span v-if="sess.client_name"> · {{ sess.client_name }}</span></div>
        <div class="actions-col">
          <button class="big danger-fill" :disabled="busy" @click="step = 'out'">Clock out</button>
          <button class="big" :disabled="busy" @click="startSwitch">Switch project / task</button>
          <button class="ghost small" @click="cancelClock">Clocked in by mistake? Discard</button>
        </div>
      </div>

      <!-- Clock out -->
      <div v-else-if="step === 'out' && sess" class="card">
        <h2>Clock out</h2>
        <p class="muted" style="margin-top: 0">{{ sess.project_name }} · {{ sess.task_title ?? 'No specific task' }} · {{ elapsed }}</p>
        <label class="field">
          Break taken (minutes)
          <div class="chips">
            <button v-for="m in [0, 15, 30, 45, 60]" :key="m" class="chip" :class="{ on: breakMinutes === m }" @click="breakMinutes = m">{{ m }}</button>
            <input v-model.number="breakMinutes" type="number" min="0" step="5" style="width: 90px" aria-label="Break minutes" />
          </div>
        </label>
        <label class="field" style="margin-top: 12px">Notes <span class="hint">optional — what got done</span><textarea v-model="outNotes" rows="2"></textarea></label>
        <div class="actions-col">
          <button class="big danger-fill" :disabled="busy" @click="clockOut">Clock out now</button>
          <button class="big" @click="step = 'running'">Back</button>
        </div>
      </div>

      <!-- Step 1: project -->
      <div v-else-if="step === 'project'" class="card">
        <div class="steps"><span class="on">1 Project</span><span>2 Task</span></div>
        <h2>{{ switching ? 'Switch to which project?' : 'Which project?' }}</h2>
        <input v-if="projects.length > 6" v-model="search" type="search" placeholder="Search projects" class="search" />
        <div class="choices">
          <button
            v-for="p in filteredProjects" :key="p.id" class="choice" :class="{ on: p.id === projectId }"
            @click="pickProject(p.id)"
          >
            <span class="grow">
              <strong>{{ p.name }}</strong>
              <span class="small muted">{{ p.client_name ?? '' }}</span>
            </span>
            <span v-if="p.id === state.last?.project_id" class="badge accent">last used</span>
          </button>
        </div>
        <div v-if="!projects.length" class="empty">
          No open projects yet. {{ isManager ? 'Create one on the Projects page.' : 'Ask an admin to add one.' }}
        </div>
        <div class="actions-col">
          <button class="big primary" :disabled="!projectId || busy" @click="toTasks">Next: choose task →</button>
          <button v-if="switching" class="big" @click="switching = false; step = 'running'">Cancel</button>
        </div>
      </div>

      <!-- Step 2: task -->
      <div v-else-if="step === 'task'" class="card">
        <div class="steps"><span class="done" @click="step = 'project'">1 Project ✓</span><span class="on">2 Task</span></div>
        <h2>{{ selectedProject?.name }}</h2>
        <p class="muted" style="margin-top: -6px">What are you working on?</p>
        <div class="choices">
          <button v-for="t in tasks" :key="t.id" class="choice" :class="{ on: t.id === taskId }" @click="taskId = t.id">
            <span class="grow">
              <strong>{{ t.title }}</strong>
              <span v-if="t.assignee_name" class="small muted">👤 {{ t.assignee_name }}</span>
            </span>
            <span v-if="t.id === state.last?.task_id && t.project_id === state.last?.project_id" class="badge accent">last used</span>
          </button>
          <button class="choice" :class="{ on: taskId === null }" @click="taskId = null">
            <span class="grow"><strong>No specific task</strong></span>
          </button>
        </div>
        <label v-if="switching" class="field" style="margin-top: 12px">
          Break taken so far (minutes)
          <input v-model.number="breakMinutes" type="number" min="0" step="5" />
        </label>
        <label class="field" style="margin-top: 12px">Note <span class="hint">optional</span><input v-model="notes" placeholder="e.g. materials run first" /></label>
        <div class="actions-col">
          <button class="big go" :disabled="busy" @click="clockIn">
            {{ switching ? 'Switch' : 'Clock in' }} — {{ selectedTask?.title ?? selectedProject?.name }}
          </button>
          <button class="big" @click="step = 'project'">← Back</button>
        </div>
      </div>
    </template>
    <p v-if="session.me" class="small muted center">
      <RouterLink to="/timesheet">See my week →</RouterLink>
    </p>
  </div>
</template>

<style scoped>
.clock { max-width: 560px; margin: 0 auto; }
.center { text-align: center; }
.running { text-align: center; padding: 28px 20px; }
.timer { font-size: 52px; font-weight: 700; font-variant-numeric: tabular-nums; letter-spacing: 1px; margin: 6px 0 10px; }
.now-project { font-size: 18px; font-weight: 650; }
.actions-col { display: flex; flex-direction: column; gap: 10px; margin-top: 20px; }
.big { justify-content: center; padding: 14px; font-size: 16px; width: 100%; }
.go { background: var(--good); border-color: var(--good); color: #fff; }
.go:hover { background: var(--good); filter: brightness(1.05); }
.danger-fill { background: var(--bad); border-color: var(--bad); color: #fff; }
.danger-fill:hover { background: var(--bad); filter: brightness(1.05); }
.steps { display: flex; gap: 14px; font-size: 12px; font-weight: 600; color: var(--muted); margin-bottom: 10px; }
.steps .on { color: var(--accent); }
.steps .done { color: var(--good); cursor: pointer; }
.search { width: 100%; margin-bottom: 10px; padding: 10px 12px; }
.choices { display: flex; flex-direction: column; gap: 8px; max-height: 55vh; overflow-y: auto; }
.choice { justify-content: flex-start; text-align: left; padding: 14px; white-space: normal; border-width: 2px; }
.choice .grow { display: flex; flex-direction: column; gap: 2px; font-weight: 400; }
.choice.on { border-color: var(--accent); background: var(--accent-soft); }
.chips { display: flex; gap: 6px; flex-wrap: wrap; align-items: center; }
.chip { padding: 8px 14px; }
.chip.on { background: var(--accent); color: var(--accent-ink); border-color: var(--accent); }
.done-banner { display: flex; align-items: center; gap: 8px; justify-content: space-between; margin-bottom: 16px; }
</style>
