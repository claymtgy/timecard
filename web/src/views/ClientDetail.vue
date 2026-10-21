<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { api, type Client, type Invoice, type Project } from '../api'
import ClientModal from '../components/ClientModal.vue'
import ProjectModal from '../components/ProjectModal.vue'
import { hours, label, money, rangeLabel } from '../format'

const props = defineProps<{ id: string }>()
const router = useRouter()
const client = ref<Client | null>(null)
const projects = ref<Project[]>([])
const invoices = ref<Invoice[]>([])
const editing = ref(false)
const addingProject = ref(false)

async function load() {
  const d = await api.get<{ client: Client; projects: Project[]; invoices: Invoice[] }>(`/api/clients/${props.id}`)
  client.value = d.client
  projects.value = d.projects
  invoices.value = d.invoices
}
onMounted(load)
</script>

<template>
  <template v-if="client">
    <div class="crumbs"><RouterLink to="/clients">Clients</RouterLink></div>
    <div class="page-head">
      <h1>{{ client.name }}</h1>
      <span v-if="!client.active" class="badge">inactive</span>
      <span class="spacer"></span>
      <button @click="editing = true">Edit</button>
    </div>
    <div class="grid cols-2">
      <div class="card">
        <h2>Contact</h2>
        <div v-if="client.contact_name"><strong>{{ client.contact_name }}</strong></div>
        <div v-if="client.phone"><a :href="`tel:${client.phone}`">{{ client.phone }}</a></div>
        <div v-if="client.email"><a :href="`mailto:${client.email}`">{{ client.email }}</a></div>
        <div v-if="client.address" style="white-space: pre-line; margin-top: 8px">{{ client.address }}</div>
        <p v-if="client.notes" class="muted" style="white-space: pre-line">{{ client.notes }}</p>
      </div>
      <div class="card">
        <h2>Balance</h2>
        <div class="stat" style="border: 0; padding: 0"><div class="k">Outstanding (sent, unpaid)</div><div class="v">{{ money(client.outstanding_cents) }}</div></div>
      </div>
    </div>

    <div class="card flush" style="margin-top: 16px">
      <div class="card-head"><h2>Projects</h2><span class="grow"></span><button class="small" @click="addingProject = true">+ New project</button></div>
      <table>
        <tbody>
          <tr v-for="p in projects" :key="p.id" class="clickable" @click="router.push(`/projects/${p.id}`)">
            <td><strong>{{ p.name }}</strong></td>
            <td><span class="badge" :class="p.status">{{ label(p.status) }}</span></td>
            <td class="num">{{ p.open_tasks }} open tasks</td>
            <td class="num">{{ hours(p.minutes) }} h</td>
          </tr>
        </tbody>
      </table>
      <div v-if="!projects.length" class="empty">No projects for this client.</div>
    </div>

    <div class="card flush">
      <div class="card-head"><h2>Invoices</h2></div>
      <table>
        <tbody>
          <tr v-for="i in invoices" :key="i.id" class="clickable" @click="router.push(`/invoices/${i.id}`)">
            <td><strong>#{{ i.number }}</strong></td>
            <td>{{ rangeLabel(i.period_start, i.period_end) || i.issue_date }}</td>
            <td class="small">{{ i.projects }}</td>
            <td class="num">{{ money(i.total_cents) }}</td>
            <td><span class="badge" :class="i.status">{{ label(i.status) }}</span></td>
          </tr>
        </tbody>
      </table>
      <div v-if="!invoices.length" class="empty">No invoices yet.</div>
    </div>
  </template>
  <ProjectModal v-if="addingProject && client" :client-id="client.id" @close="addingProject = false" @saved="(p) => router.push(`/projects/${p.id}`)" />
  <ClientModal v-if="editing && client" :client="client" @close="editing = false" @saved="editing = false; load()" @deleted="router.push('/clients')" />
</template>
