<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import { api, type Task, type TaskStatus } from '../api'
import { dateLabel, hours, label, today } from '../format'
import { isManager, session } from '../session'
import { useAction } from '../util'

const scope = ref<'me' | 'all'>(isManager.value ? 'all' : 'me')
const status = ref<'open' | '' | TaskStatus>('open')
const tasks = ref<Task[]>([])
const { error, run } = useAction()

async function load() {
  tasks.value = await api.get<Task[]>('/api/tasks', { assignee: scope.value === 'me' ? 'me' : undefined, status: status.value })
}
onMounted(load)
watch([scope, status], load)

async function setStatus(t: Task, s: TaskStatus) {
  const u = await run(() => api.patch<Task>(`/api/tasks/${t.id}`, { status: s }))
  if (u) Object.assign(t, u)
}
const canEdit = (t: Task) => isManager.value || t.assignee_worker_id === session.me?.worker_id
const overdue = (t: Task) => t.due_date && t.status !== 'done' && t.due_date < today()
</script>

<template>
  <div class="page-head">
    <h1>Tasks</h1>
    <span class="spacer"></span>
    <select v-model="scope" aria-label="Whose tasks">
      <option value="me">Assigned to me</option>
      <option value="all">Everyone</option>
    </select>
    <select v-model="status" aria-label="Status">
      <option value="open">Open</option>
      <option value="todo">To do</option>
      <option value="in_progress">In progress</option>
      <option value="blocked">Blocked</option>
      <option value="done">Done</option>
      <option value="">All</option>
    </select>
  </div>
  <div v-if="error" class="error">{{ error }}</div>
  <div class="card flush">
    <div class="table-wrap">
      <table>
        <thead><tr><th>Task</th><th>Project</th><th>Assignee</th><th>Due</th><th class="num">Hours</th><th>Status</th></tr></thead>
        <tbody>
          <tr v-for="t in tasks" :key="t.id">
            <td><strong>{{ t.title }}</strong></td>
            <td><RouterLink :to="`/projects/${t.project_id}`">{{ t.project_name }}</RouterLink></td>
            <td>{{ t.assignee_name ?? '—' }}</td>
            <td class="nowrap" :class="{ neg: overdue(t) }">{{ dateLabel(t.due_date) }}</td>
            <td class="num">{{ hours(t.minutes) }}</td>
            <td>
              <select v-if="canEdit(t)" :value="t.status" @change="setStatus(t, ($event.target as HTMLSelectElement).value as TaskStatus)">
                <option value="todo">To do</option>
                <option value="in_progress">In progress</option>
                <option value="blocked">Blocked</option>
                <option value="done">Done</option>
              </select>
              <span v-else class="badge" :class="t.status">{{ label(t.status) }}</span>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <div v-if="!tasks.length" class="empty">No tasks.</div>
  </div>
</template>
