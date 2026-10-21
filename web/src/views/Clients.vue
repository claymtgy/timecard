<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { api, type Client } from '../api'
import ClientModal from '../components/ClientModal.vue'
import { money } from '../format'

const router = useRouter()
const clients = ref<Client[]>([])
const creating = ref(false)

onMounted(async () => {
  clients.value = await api.get<Client[]>('/api/clients')
})
</script>

<template>
  <div class="page-head">
    <h1>Clients</h1>
    <span class="spacer"></span>
    <button class="primary" @click="creating = true">+ New client</button>
  </div>
  <div class="card flush">
    <table>
      <thead><tr><th>Name</th><th>Contact</th><th>Phone</th><th>Email</th><th class="num">Projects</th><th class="num">Outstanding</th></tr></thead>
      <tbody>
        <tr v-for="c in clients" :key="c.id" class="clickable" @click="router.push(`/clients/${c.id}`)">
          <td><strong>{{ c.name }}</strong> <span v-if="!c.active" class="badge">inactive</span></td>
          <td>{{ c.contact_name }}</td>
          <td class="nowrap">{{ c.phone }}</td>
          <td>{{ c.email }}</td>
          <td class="num">{{ c.project_count }}</td>
          <td class="num">{{ c.outstanding_cents ? money(c.outstanding_cents) : '' }}</td>
        </tr>
      </tbody>
    </table>
    <div v-if="!clients.length" class="empty">No clients yet — add the builders and homeowners you invoice.</div>
  </div>
  <ClientModal v-if="creating" @close="creating = false" @saved="(c) => router.push(`/clients/${c.id}`)" />
</template>
