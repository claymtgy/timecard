<script setup lang="ts">
import { ref } from 'vue'
import { api, type Client } from '../api'
import { confirmDelete, useAction } from '../util'
import Modal from './Modal.vue'

const props = defineProps<{ client?: Client | null }>()
const emit = defineEmits<{ close: []; saved: [Client]; deleted: [] }>()
const c = props.client
const form = ref({
  name: c?.name ?? '', contact_name: c?.contact_name ?? '', email: c?.email ?? '', phone: c?.phone ?? '',
  address: c?.address ?? '', notes: c?.notes ?? '', active: c?.active ?? true,
})
const { error, busy, run } = useAction()

async function save() {
  const saved = await run(() => (c ? api.put<Client>(`/api/clients/${c.id}`, form.value) : api.post<Client>('/api/clients', form.value)))
  if (saved) emit('saved', saved)
}
async function remove() {
  if (!c || !confirmDelete(c.name)) return
  if (await run(() => api.del(`/api/clients/${c.id}`).then(() => true))) emit('deleted')
}
</script>

<template>
  <Modal :title="c ? 'Edit client' : 'New client'" @close="emit('close')">
    <div v-if="error" class="error">{{ error }}</div>
    <form class="form-grid" @submit.prevent="save">
      <label class="field full">Name<input v-model="form.name" required placeholder="Company or person" /></label>
      <label class="field">Contact<input v-model="form.contact_name" /></label>
      <label class="field">Phone<input v-model="form.phone" type="tel" /></label>
      <label class="field full">Email<input v-model="form.email" type="email" /></label>
      <label class="field full">Address<textarea v-model="form.address" rows="2"></textarea></label>
      <label class="field full">Notes<textarea v-model="form.notes" rows="3"></textarea></label>
      <label class="check"><input v-model="form.active" type="checkbox" /> Active</label>
      <button type="submit" hidden></button>
    </form>
    <template #foot>
      <button v-if="c" class="danger" @click="remove">Delete</button>
      <span class="grow"></span>
      <button @click="emit('close')">Cancel</button>
      <button class="primary" :disabled="busy" @click="save">Save</button>
    </template>
  </Modal>
</template>
