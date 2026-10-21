<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api, type TaskTemplate, type TaskTemplateItem } from '../api'
import { hours } from '../format'
import { confirmDelete, useAction } from '../util'

// Reusable task lists (e.g. "Roof replacement": tear-off, dry in, shingling, cleanup).
const templates = ref<TaskTemplate[]>([])
const editing = ref<{ id?: number; name: string; description: string; items: (TaskTemplateItem & { estimate: string })[] } | null>(null)
const newItem = ref('')
const { error, busy, run } = useAction()

async function load() {
  templates.value = await api.get<TaskTemplate[]>('/api/templates')
}
onMounted(load)

function edit(t?: TaskTemplate) {
  editing.value = t
    ? { id: t.id, name: t.name, description: t.description, items: t.items.map((i) => ({ ...i, estimate: i.estimate_minutes != null ? hours(i.estimate_minutes) : '' })) }
    : { name: '', description: '', items: [] }
  newItem.value = ''
}
function addItem() {
  const title = newItem.value.trim()
  if (!title || !editing.value) return
  editing.value.items.push({ title, description: '', estimate_minutes: null, estimate: '' })
  newItem.value = ''
}
function move(i: number, d: number) {
  const items = editing.value!.items
  const j = i + d
  if (j < 0 || j >= items.length) return
  ;[items[i], items[j]] = [items[j], items[i]]
}
async function save() {
  const e = editing.value!
  const body = {
    name: e.name,
    description: e.description,
    items: e.items.map((i) => ({ title: i.title, description: i.description, estimate_minutes: i.estimate === '' ? null : Math.round(Number(i.estimate) * 60) })),
  }
  const saved = await run(() => (e.id ? api.put<TaskTemplate>(`/api/templates/${e.id}`, body) : api.post<TaskTemplate>('/api/templates', body)))
  if (saved) {
    editing.value = null
    await load()
  }
}
async function remove(t: TaskTemplate) {
  if (!confirmDelete(`template “${t.name}” (projects keep their tasks)`)) return
  if (await run(() => api.del(`/api/templates/${t.id}`).then(() => true))) await load()
}
</script>

<template>
  <div class="page-head">
    <h1>Task templates</h1>
    <span class="spacer"></span>
    <button v-if="!editing" class="primary" @click="edit()">+ New template</button>
  </div>
  <p class="muted" style="margin-top: -8px">
    A template is a reusable list of tasks. Pick one when creating a project (or “Add from template” on a project), then add project-specific tasks.
  </p>
  <div v-if="error" class="error">{{ error }}</div>

  <div v-if="editing" class="card">
    <div class="form-grid">
      <label class="field">Name<input v-model="editing.name" placeholder="e.g. Roof replacement" /></label>
      <label class="field">Description<input v-model="editing.description" /></label>
    </div>
    <h3 style="margin-top: 16px">Tasks</h3>
    <table>
      <thead><tr><th style="width: 40px">#</th><th>Task</th><th style="width: 120px">Estimate (h)</th><th></th></tr></thead>
      <tbody>
        <tr v-for="(it, i) in editing.items" :key="i">
          <td class="muted">{{ i + 1 }}</td>
          <td><input v-model="it.title" style="width: 100%" /></td>
          <td><input v-model="it.estimate" inputmode="decimal" style="width: 100%" /></td>
          <td class="actions">
            <button class="small ghost" :disabled="i === 0" aria-label="Move up" @click="move(i, -1)">↑</button>
            <button class="small ghost" :disabled="i === editing.items.length - 1" aria-label="Move down" @click="move(i, 1)">↓</button>
            <button class="small ghost danger" @click="editing.items.splice(i, 1)">Remove</button>
          </td>
        </tr>
      </tbody>
    </table>
    <form class="row" style="margin-top: 10px" @submit.prevent="addItem">
      <input v-model="newItem" class="grow" placeholder="Add a task and press Enter" />
      <button :disabled="!newItem.trim()">Add</button>
    </form>
    <div class="row" style="margin-top: 16px; justify-content: flex-end">
      <button @click="editing = null">Cancel</button>
      <button class="primary" :disabled="busy || !editing.name.trim()" @click="save">Save template</button>
    </div>
  </div>

  <div v-else class="grid cols-2">
    <div v-for="t in templates" :key="t.id" class="card">
      <div class="row">
        <h2 class="grow" style="margin: 0">{{ t.name }}</h2>
        <button class="small" @click="edit(t)">Edit</button>
        <button class="small ghost danger" @click="remove(t)">Delete</button>
      </div>
      <p v-if="t.description" class="small muted">{{ t.description }}</p>
      <ol class="small" style="padding-left: 20px; margin-bottom: 0">
        <li v-for="it in t.items" :key="it.id">{{ it.title }}<span v-if="it.estimate_minutes" class="muted"> · {{ hours(it.estimate_minutes) }} h</span></li>
      </ol>
    </div>
    <div v-if="!templates.length" class="card empty">No templates yet.</div>
  </div>
</template>
