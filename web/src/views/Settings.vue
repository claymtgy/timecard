<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api, type CompanySettings, type Settings } from '../api'
import { isAdmin } from '../session'
import { useAction } from '../util'

const s = ref<Settings | null>(null)
const form = ref<CompanySettings | null>(null)
const saved = ref(false)
const { error, busy, run } = useAction()

onMounted(async () => {
  s.value = await api.get<Settings>('/api/settings')
  form.value = { ...s.value.company }
})
async function save() {
  const r = await run(() => api.put<Settings>('/api/settings', form.value))
  if (r) {
    s.value = r
    saved.value = true
    setTimeout(() => (saved.value = false), 2500)
  }
}
</script>

<template>
  <div class="page-head"><h1>Company &amp; email</h1></div>
  <div v-if="error" class="error">{{ error }}</div>
  <template v-if="s && form">
    <div class="card">
      <h2>Shown on invoices</h2>
      <fieldset :disabled="!isAdmin" style="border: 0; padding: 0; margin: 0">
        <div class="form-grid">
          <label class="field">Company name<input v-model="form.name" placeholder="e.g. M&M Roofing" /></label>
          <label class="field">Phone<input v-model="form.phone" type="tel" /></label>
          <label class="field">Email <span class="hint">clients' replies go here</span><input v-model="form.email" type="email" /></label>
          <label class="field">Payment due (days after issue)<input v-model.number="form.default_due_days" type="number" min="0" max="365" /></label>
          <label class="field full">Address<textarea v-model="form.address" rows="3"></textarea></label>
          <label class="field full">Payment instructions / footer<textarea v-model="form.invoice_footer" rows="3" placeholder="e.g. Make checks payable to … · Zelle: …"></textarea></label>
        </div>
      </fieldset>
      <div v-if="isAdmin" class="row" style="margin-top: 14px; justify-content: flex-end">
        <span v-if="saved" class="pos small">Saved</span>
        <button class="primary" :disabled="busy" @click="save">Save</button>
      </div>
      <p v-else class="small muted">Only admins can change these.</p>
    </div>
    <div class="card">
      <h2>Email</h2>
      <p v-if="s.email_configured">
        Invoices are emailed from <strong>{{ s.email_from }}</strong> with the PDF attached.
      </p>
      <p v-else class="neg">
        Email isn't configured on the server, so invoices can only be downloaded as PDF. Set <code>SMTP_HOST</code>,
        <code>SMTP_PORT</code>, <code>SMTP_USERNAME</code>, <code>SMTP_PASSWORD</code> and <code>SMTP_FROM</code> in <code>.env</code> and restart the app.
      </p>
    </div>
  </template>
</template>
