<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { api, type Project, type ProjectReport, type Task, type TaskStatus, type TaskTemplate, type TimeEntry } from '../api'
import EntryModal from '../components/EntryModal.vue'
import Modal from '../components/Modal.vue'
import ProjectModal from '../components/ProjectModal.vue'
import { dateLabel, dayLabel, hours, label, money, percent, today } from '../format'
import { activeWorkers, isManager, session } from '../session'
import { confirmDelete, useAction } from '../util'

const props = defineProps<{ id: string }>()
const router = useRouter()
const project = ref<Project | null>(null)
const tasks = ref<Task[]>([])
const report = ref<ProjectReport | null>(null)
const entries = ref<TimeEntry[]>([])
const templates = ref<TaskTemplate[]>([])
const editing = ref(false)
const applying = ref(false)
const applyId = ref<number | null>(null)
const quickTask = ref('')
const taskForm = ref<{ id?: number; title: string; description: string; status: TaskStatus; assignee_worker_id: number | null; due_date: string; estimate: string } | null>(null)
const logging = ref(false)
const editingEntry = ref<TimeEntry | null>(null)
const notice = ref('')
const { error, busy, run } = useAction()

async function load() {
  const d = await api.get<{ project: Project; tasks: Task[]; report?: ProjectReport }>(`/api/projects/${props.id}`)
  project.value = d.project
  tasks.value = d.tasks
  report.value = d.report ?? null
  entries.value = await api.get<TimeEntry[]>('/api/time-entries', { project_id: props.id })
}
onMounted(load)

const lanes: { status: TaskStatus; title: string }[] = [
  { status: 'todo', title: 'To do' },
  { status: 'in_progress', title: 'In progress' },
  { status: 'blocked', title: 'Blocked' },
  { status: 'done', title: 'Done' },
]
const tasksIn = (s: TaskStatus) => tasks.value.filter((t) => t.status === s)
const workerCols = computed(() => report.value?.workers ?? [])
const phaseMinutes = (ph: ProjectReport['phases'][number], workerId: number) => ph.workers.find((w) => w.worker_id === workerId)?.minutes ?? 0

// ---- tasks ----
const dragging = ref<Task | null>(null)
const dropLane = ref<TaskStatus | null>(null)
const canMove = (t: Task) => isManager.value || (t.assignee_worker_id != null && t.assignee_worker_id === session.me?.worker_id)

async function moveTask(t: Task, status: TaskStatus) {
  if (t.status === status || !canMove(t)) return
  const prev = t.status
  t.status = status
  if (!(await run(() => api.patch<Task>(`/api/tasks/${t.id}`, { status })))) t.status = prev
}
function onDrop(status: TaskStatus) {
  if (dragging.value) moveTask(dragging.value, status)
  dragging.value = null
  dropLane.value = null
}
async function addQuickTask() {
  const title = quickTask.value.trim()
  if (!title) return
  if (await run(() => api.post<Task>(`/api/projects/${props.id}/tasks`, { title }))) {
    quickTask.value = ''
    await load()
  }
}
function editTask(t: Task) {
  if (!isManager.value) return
  taskForm.value = {
    id: t.id, title: t.title, description: t.description, status: t.status, assignee_worker_id: t.assignee_worker_id,
    due_date: t.due_date ?? '', estimate: t.estimate_minutes != null ? hours(t.estimate_minutes) : '',
  }
}
async function saveTask() {
  const f = taskForm.value!
  const estimate = f.estimate === '' ? -1 : Math.round(Number(f.estimate) * 60)
  const saved = await run(() =>
    api.patch<Task>(`/api/tasks/${f.id}`, {
      title: f.title, description: f.description, status: f.status, assignee_worker_id: f.assignee_worker_id,
      clear_assignee: f.assignee_worker_id == null, due_date: f.due_date || '', estimate_minutes: estimate,
    }),
  )
  if (saved) {
    taskForm.value = null
    await load()
  }
}
async function deleteTask() {
  const f = taskForm.value
  if (!f?.id || !confirmDelete('this task')) return
  if (await run(() => api.del(`/api/tasks/${f.id}`).then(() => true))) {
    taskForm.value = null
    await load()
  }
}

// ---- templates ----
async function openApply() {
  templates.value = await api.get<TaskTemplate[]>('/api/templates')
  applyId.value = templates.value[0]?.id ?? null
  applying.value = true
}
async function applyTemplate() {
  const r = await run(() => api.post<{ added: number; tasks: Task[] }>(`/api/projects/${props.id}/apply-template`, { template_id: applyId.value }))
  if (r) {
    tasks.value = r.tasks
    applying.value = false
    notice.value = r.added ? `Added ${r.added} task${r.added === 1 ? '' : 's'} from the template.` : 'All of that template’s tasks were already on this project.'
  }
}
async function saveAsTemplate() {
  const name = window.prompt('Template name', project.value?.name ?? '')
  if (!name) return
  if (await run(() => api.post(`/api/projects/${props.id}/save-as-template`, { name }))) notice.value = `Saved “${name}” as a task template.`
}

async function postShare() {
  const r = await run(() => api.post<ProjectReport>(`/api/projects/${props.id}/profit-share`))
  if (r) report.value = r
}
async function deleteProject() {
  if (!confirmDelete('this project')) return
  if (await run(() => api.del(`/api/projects/${props.id}`).then(() => true))) router.push('/projects')
}
</script>

<template>
  <template v-if="project">
    <div class="crumbs"><RouterLink to="/projects">Projects</RouterLink></div>
    <div class="page-head">
      <h1>{{ project.name }}</h1>
      <span class="badge" :class="project.billing_type">{{ project.billing_type === 'fixed' ? 'Bid' : 'Hourly' }}</span>
      <span class="badge" :class="project.status">{{ label(project.status) }}</span>
      <RouterLink v-if="project.client_id && isManager" :to="`/clients/${project.client_id}`" class="muted">{{ project.client_name }}</RouterLink>
      <span v-else-if="project.client_name" class="muted">{{ project.client_name }}</span>
      <span class="spacer"></span>
      <button @click="logging = true">+ Add time</button>
      <template v-if="isManager">
        <button @click="editing = true">Edit</button>
        <button v-if="!entries.length" class="danger" @click="deleteProject">Delete</button>
      </template>
    </div>
    <p v-if="project.description" class="muted">{{ project.description }}</p>
    <div v-if="error" class="error">{{ error }}</div>
    <div v-if="notice" class="card row" style="margin-bottom: 16px"><span class="grow">{{ notice }}</span><button class="small ghost" @click="notice = ''">✕</button></div>

    <!-- Money (managers) -->
    <div v-if="report" class="stats">
      <div class="stat"><div class="k">Hours</div><div class="v">{{ hours(report.minutes) }}</div></div>
      <template v-if="report.billing_type === 'fixed'">
        <div class="stat"><div class="k">Bid</div><div class="v">{{ money(report.bid_amount_cents) }}</div></div>
        <div class="stat">
          <div class="k">Labor cost <span v-if="report.labor_cost_rate_cents">@ {{ money(report.labor_cost_rate_cents) }}/h</span></div>
          <div class="v">{{ money(report.labor_cost_cents) }}</div>
        </div>
        <div class="stat"><div class="k">Profit</div><div class="v" :class="report.profit_cents < 0 ? 'neg' : 'pos'">{{ money(report.profit_cents) }}</div></div>
        <div v-if="report.profit_share_bps" class="stat">
          <div class="k">{{ percent(report.profit_share_bps) }} share → {{ report.profit_share_worker_name }}</div>
          <div class="v">{{ money(report.profit_share_cents) }}</div>
          <div class="small muted">Total pay on project {{ money(report.share_worker_pay_cents) }}</div>
        </div>
      </template>
      <template v-else>
        <div class="stat"><div class="k">Billable at {{ money(report.bill_rate_cents) }}/h</div><div class="v">{{ money(report.revenue_cents) }}</div></div>
        <div class="stat"><div class="k">Wages</div><div class="v">{{ money(report.wages_cents) }}</div></div>
        <div class="stat"><div class="k">Margin</div><div class="v" :class="report.profit_cents < 0 ? 'neg' : 'pos'">{{ money(report.profit_cents) }}</div></div>
        <div class="stat"><div class="k">Not yet invoiced</div><div class="v">{{ hours(project.unbilled_minutes) }} h</div></div>
      </template>
      <div class="stat"><div class="k">Invoiced</div><div class="v">{{ money(report.invoiced_cents) }}</div><div class="small muted">{{ money(report.paid_cents) }} paid</div></div>
    </div>
    <div v-if="report && report.billing_type === 'fixed' && report.profit_share_bps" class="card row" style="margin-bottom: 16px">
      <span v-if="report.posted_share_cents == null" class="grow">Profit share not yet added to payroll.</span>
      <span v-else class="grow">
        Profit share of {{ money(report.posted_share_cents) }} is on payroll{{ report.posted_share_paid ? ' (paid)' : '' }}.
        <span v-if="report.posted_share_cents !== report.profit_share_cents && !report.posted_share_paid" class="neg">Hours changed since — update it.</span>
      </span>
      <button v-if="!report.posted_share_paid" class="primary" :disabled="busy" @click="postShare">
        {{ report.posted_share_cents == null ? 'Add to payroll' : 'Update payroll amount' }}
      </button>
    </div>

    <!-- Tasks -->
    <div class="page-head" style="margin: 8px 0 10px">
      <h2 style="margin: 0">Tasks</h2>
      <span class="spacer"></span>
      <template v-if="isManager">
        <button @click="openApply">Add from template…</button>
        <button v-if="tasks.length" @click="saveAsTemplate">Save as template</button>
      </template>
    </div>
    <form v-if="isManager" class="row" style="margin-bottom: 12px" @submit.prevent="addQuickTask">
      <input v-model="quickTask" class="grow" placeholder="Add a task (e.g. Dry in, Shingling, Punch list) and press Enter" />
      <button class="primary" :disabled="!quickTask.trim() || busy">Add task</button>
    </form>
    <div v-if="!tasks.length" class="card empty">
      No tasks yet. {{ isManager ? 'Add them above or start from a template — workers pick a task when they clock in.' : '' }}
    </div>
    <div v-else class="board">
      <div
        v-for="lane in lanes" :key="lane.status" class="lane" :class="{ drop: dropLane === lane.status }"
        @dragover.prevent="dropLane = lane.status" @dragleave="dropLane = null" @drop="onDrop(lane.status)"
      >
        <div class="lane-head"><span>{{ lane.title }}</span><span class="muted">{{ tasksIn(lane.status).length }}</span></div>
        <div v-for="t in tasksIn(lane.status)" :key="t.id" class="task-card" :draggable="canMove(t)" @dragstart="dragging = t" @click="editTask(t)">
          <div class="t">{{ t.title }}</div>
          <div class="m">
            <span v-if="t.assignee_name">👤 {{ t.assignee_name }}</span>
            <span v-if="t.due_date">📅 {{ dateLabel(t.due_date) }}</span>
            <span v-if="t.minutes || t.estimate_minutes">⏱ {{ hours(t.minutes) }}<template v-if="t.estimate_minutes"> / {{ hours(t.estimate_minutes) }}</template> h</span>
          </div>
        </div>
      </div>
    </div>

    <!-- Hours by task x worker -->
    <div v-if="report && report.phases.length" class="card flush" style="margin-top: 16px">
      <div class="card-head"><h2>Hours by task</h2></div>
      <div class="table-wrap">
        <table>
          <thead><tr><th>Task</th><th v-for="w in workerCols" :key="w.worker_id" class="num">{{ w.worker_name }}</th><th class="num">Total</th></tr></thead>
          <tbody>
            <tr v-for="ph in report.phases" :key="ph.task_id ?? 0">
              <td>{{ ph.title }}</td>
              <td v-for="w in workerCols" :key="w.worker_id" class="num">{{ hours(phaseMinutes(ph, w.worker_id)) }}</td>
              <td class="num">{{ hours(ph.minutes) }}</td>
            </tr>
          </tbody>
          <tfoot>
            <tr><td>Total</td><td v-for="w in workerCols" :key="w.worker_id" class="num">{{ hours(w.minutes) }}</td><td class="num">{{ hours(report.minutes) }}</td></tr>
          </tfoot>
        </table>
      </div>
    </div>

    <!-- Time -->
    <div class="card flush" style="margin-top: 16px">
      <div class="card-head"><h2>Time on this project</h2><span class="muted small">{{ entries.length }} entries</span></div>
      <div class="table-wrap">
        <table>
          <thead><tr><th>Date</th><th>Worker</th><th>Task</th><th class="num">Hours</th><th>Notes</th><th></th></tr></thead>
          <tbody>
            <tr v-for="e in [...entries].reverse()" :key="e.id" class="clickable" @click="editingEntry = e">
              <td class="nowrap">{{ dayLabel(e.work_date) }}</td>
              <td>{{ e.worker_name }}</td>
              <td>{{ e.task_title ?? '' }}</td>
              <td class="num">{{ hours(e.minutes) }}</td>
              <td class="small">{{ e.notes }}</td>
              <td class="actions"><span v-if="e.invoice_number" class="badge sent">Inv #{{ e.invoice_number }}</span></td>
            </tr>
          </tbody>
        </table>
      </div>
      <div v-if="!entries.length" class="empty">No time logged yet.</div>
    </div>
  </template>

  <ProjectModal v-if="editing && project" :project="project" @close="editing = false" @saved="editing = false; load()" />
  <EntryModal v-if="logging && project" :defaults="{ project_id: project.id, work_date: today() }" @close="logging = false" @saved="logging = false; load()" />
  <EntryModal v-if="editingEntry" :entry="editingEntry" @close="editingEntry = null" @saved="editingEntry = null; load()" @deleted="editingEntry = null; load()" />

  <Modal v-if="applying" title="Add tasks from a template" @close="applying = false">
    <div v-if="error" class="error">{{ error }}</div>
    <div v-if="!templates.length" class="empty">No templates yet. Create them under Task templates.</div>
    <template v-else>
      <label class="field">
        Template
        <select v-model="applyId">
          <option v-for="t in templates" :key="t.id" :value="t.id">{{ t.name }} ({{ t.items.length }} tasks)</option>
        </select>
      </label>
      <ol class="small" style="margin: 12px 0 0; padding-left: 20px">
        <li v-for="(it, i) in templates.find((t) => t.id === applyId)?.items ?? []" :key="i">{{ it.title }}</li>
      </ol>
      <p class="small muted">Tasks the project already has (same title) are skipped. You can add more tasks afterwards.</p>
    </template>
    <template #foot>
      <button @click="applying = false">Cancel</button>
      <button class="primary" :disabled="!applyId || busy" @click="applyTemplate">Add tasks</button>
    </template>
  </Modal>

  <Modal v-if="taskForm" title="Edit task" @close="taskForm = null">
    <div v-if="error" class="error">{{ error }}</div>
    <form class="form-grid" @submit.prevent="saveTask">
      <label class="field full">Title<input v-model="taskForm.title" required /></label>
      <label class="field">Status<select v-model="taskForm.status"><option v-for="l in lanes" :key="l.status" :value="l.status">{{ l.title }}</option></select></label>
      <label class="field">
        Assignee
        <select v-model="taskForm.assignee_worker_id">
          <option :value="null">—</option>
          <option v-for="w in activeWorkers" :key="w.id" :value="w.id">{{ w.name }}</option>
        </select>
      </label>
      <label class="field">Due<input v-model="taskForm.due_date" type="date" /></label>
      <label class="field">Estimate (h)<input v-model="taskForm.estimate" inputmode="decimal" /></label>
      <label class="field full">Details<textarea v-model="taskForm.description"></textarea></label>
      <button type="submit" hidden></button>
    </form>
    <template #foot>
      <button class="danger" @click="deleteTask">Delete</button>
      <span class="grow"></span>
      <button @click="taskForm = null">Cancel</button>
      <button class="primary" :disabled="busy" @click="saveTask">Save</button>
    </template>
  </Modal>
</template>
