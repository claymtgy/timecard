<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { api, type Project } from '../api'
import ProjectModal from '../components/ProjectModal.vue'
import { hours, label, money } from '../format'
import { isManager } from '../session'

const router = useRouter()
const projects = ref<Project[]>([])
const showClosed = ref(false)
const creating = ref(false)

onMounted(async () => {
  projects.value = await api.get<Project[]>('/api/projects')
})
const visible = computed(() => projects.value.filter((p) => showClosed.value || p.status === 'active' || p.status === 'planned'))
function terms(p: Project) {
  if (!isManager.value) return ''
  return p.billing_type === 'fixed' ? `${money(p.bid_amount_cents)} bid` : `${money(p.bill_rate_cents)}/h`
}
</script>

<template>
  <div class="page-head">
    <h1>Projects</h1>
    <span class="spacer"></span>
    <label class="check"><input v-model="showClosed" type="checkbox" /> Show done &amp; cancelled</label>
    <button v-if="isManager" class="primary" @click="creating = true">+ New project</button>
  </div>
  <div class="card flush">
    <div class="table-wrap">
      <table>
        <thead>
          <tr><th>Project</th><th>Client</th><th>Billing</th><th>Status</th><th class="num">Open tasks</th><th class="num">Hours</th><th class="num">Unbilled h</th></tr>
        </thead>
        <tbody>
          <tr v-for="p in visible" :key="p.id" class="clickable" @click="router.push(`/projects/${p.id}`)">
            <td><strong>{{ p.name }}</strong></td>
            <td>{{ p.client_name ?? '—' }}</td>
            <td><span class="badge" :class="p.billing_type">{{ p.billing_type === 'fixed' ? 'Bid' : 'Hourly' }}</span> <span class="small">{{ terms(p) }}</span></td>
            <td><span class="badge" :class="p.status">{{ label(p.status) }}</span></td>
            <td class="num">{{ p.open_tasks }} / {{ p.task_count }}</td>
            <td class="num">{{ hours(p.minutes) }}</td>
            <td class="num">{{ p.billing_type === 'hourly' ? hours(p.unbilled_minutes) : '—' }}</td>
          </tr>
        </tbody>
      </table>
    </div>
    <div v-if="!visible.length" class="empty">
      No projects yet.{{ isManager ? ' Create one — hourly (billed by the hour) or by bid (fixed price) — and add its tasks.' : '' }}
    </div>
  </div>
  <ProjectModal v-if="creating" @close="creating = false" @saved="(p) => router.push(`/projects/${p.id}`)" />
</template>
