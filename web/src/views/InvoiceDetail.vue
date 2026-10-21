<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { api, type InvoiceDetail, type InvoiceStatus, type Settings } from '../api'
import Modal from '../components/Modal.vue'
import { dateLabel, dayLabel, dollarsToCents, hours, label, money, rangeLabel, today } from '../format'
import { confirmDelete, useAction } from '../util'

const props = defineProps<{ id: string }>()
const router = useRouter()
const d = ref<InvoiceDetail | null>(null)
const settings = ref<Settings | null>(null)
const showEntries = ref(false)
const addingLine = ref(false)
const lineForm = ref({ description: '', quantity: '1', unit_price: '' })
const sendForm = ref<{ to: string; cc: string; subject: string; message: string } | null>(null)
const sentNotice = ref('')
const { error, busy, run } = useAction()

onMounted(async () => {
  ;[d.value, settings.value] = await Promise.all([
    api.get<InvoiceDetail>(`/api/invoices/${props.id}`),
    api.get<Settings>('/api/settings'),
  ])
})
const inv = computed(() => d.value?.invoice)
const pdfUrl = computed(() => `/api/invoices/${props.id}/pdf`)

async function setStatus(status: InvoiceStatus) {
  if (status === 'void' && !window.confirm('Void this invoice? Its hours become unbilled again.')) return
  const body: Record<string, string> = { status }
  if (status === 'paid') {
    const paid = window.prompt('Payment date (YYYY-MM-DD)', today())
    if (!paid) return
    body.paid_date = paid
  }
  const r = await run(() => api.patch<InvoiceDetail>(`/api/invoices/${props.id}`, body))
  if (r) d.value = r
}
async function remove() {
  if (!confirmDelete(`invoice #${inv.value?.number}`)) return
  if (await run(() => api.del(`/api/invoices/${props.id}`).then(() => true))) router.push('/invoices')
}
async function addLine() {
  const f = lineForm.value
  const r = await run(() =>
    api.post<InvoiceDetail>(`/api/invoices/${props.id}/lines`, {
      description: f.description, quantity: Number(f.quantity) || 1, unit_price_cents: dollarsToCents(f.unit_price),
    }),
  )
  if (r) {
    d.value = r
    addingLine.value = false
    lineForm.value = { description: '', quantity: '1', unit_price: '' }
  }
}
async function removeLine(id: number) {
  if (!window.confirm('Remove this line? Hours on it become unbilled again.')) return
  const r = await run(() => api.del(`/api/invoices/${props.id}/lines/${id}`).then(() => api.get<InvoiceDetail>(`/api/invoices/${props.id}`)))
  if (r) d.value = r
}
async function saveNotes(notes: string) {
  const r = await run(() => api.patch<InvoiceDetail>(`/api/invoices/${props.id}`, { notes }))
  if (r) d.value = r
}

function openSend() {
  const i = inv.value!
  const company = settings.value?.company.name || 'us'
  const due = i.due_date ? ` It is due ${dateLabel(i.due_date)}.` : ''
  sendForm.value = {
    to: i.client_email ?? '',
    cc: '',
    subject: `Invoice ${i.number} from ${company}`,
    message: `Hello,\n\nPlease find attached invoice ${i.number} for ${money(i.total_cents)}.${due}\n\nThank you for your business!\n${company}`,
  }
}
async function send() {
  const r = await run(() => api.post<InvoiceDetail>(`/api/invoices/${props.id}/send`, sendForm.value))
  if (r) {
    d.value = r
    sentNotice.value = `Sent to ${r.invoice.sent_to}.`
    sendForm.value = null
  }
}
</script>

<template>
  <template v-if="d && inv">
    <div class="crumbs"><RouterLink to="/invoices">Invoices</RouterLink></div>
    <div class="page-head">
      <h1>Invoice #{{ inv.number }}</h1>
      <span class="badge" :class="inv.status">{{ label(inv.status) }}</span>
      <span v-if="inv.sent_at" class="small muted">emailed {{ new Date(inv.sent_at).toLocaleDateString() }} to {{ inv.sent_to }}</span>
      <span class="spacer"></span>
      <a class="btn" :href="pdfUrl" target="_blank" rel="noopener">View PDF</a>
      <a class="btn" :href="`${pdfUrl}?download=1`">Download</a>
      <button v-if="inv.status !== 'void'" class="primary" @click="openSend">{{ inv.sent_at ? 'Email again' : 'Email to client' }}</button>
    </div>
    <div v-if="error && !sendForm && !addingLine" class="error">{{ error }}</div>
    <div v-if="sentNotice" class="card row" style="margin-bottom: 16px"><span class="grow pos">✉️ {{ sentNotice }}</span><button class="small ghost" @click="sentNotice = ''">✕</button></div>

    <div class="row" style="margin-bottom: 14px">
      <template v-if="inv.status === 'draft'">
        <button @click="addingLine = true">+ Line</button>
        <button @click="setStatus('sent')">Mark sent (without emailing)</button>
        <button class="danger" @click="remove">Delete draft</button>
      </template>
      <template v-else-if="inv.status === 'sent'">
        <button class="primary" :disabled="busy" @click="setStatus('paid')">Mark paid</button>
        <button @click="setStatus('draft')">Back to draft</button>
        <button class="danger" @click="setStatus('void')">Void</button>
      </template>
      <button v-else-if="inv.status === 'paid'" @click="setStatus('sent')">Mark unpaid</button>
      <button v-else-if="inv.status === 'void'" class="danger" @click="remove">Delete</button>
    </div>

    <div class="card">
      <div class="row" style="align-items: flex-start; gap: 32px; margin-bottom: 18px">
        <div><div class="small muted">Bill to</div><strong>{{ inv.client_name ?? '—' }}</strong><div class="small">{{ inv.client_email }}</div></div>
        <div>
          <div class="small muted">Issued</div>{{ dateLabel(inv.issue_date) }}
          <template v-if="inv.due_date"><div class="small muted" style="margin-top: 6px">Due</div>{{ dateLabel(inv.due_date) }}</template>
        </div>
        <div v-if="inv.period_start || inv.period_end"><div class="small muted">Work period</div>{{ rangeLabel(inv.period_start, inv.period_end) }}</div>
        <div v-if="inv.paid_date"><div class="small muted">Paid</div>{{ dateLabel(inv.paid_date) }}</div>
      </div>

      <table>
        <thead><tr><th>Description</th><th class="num">Qty</th><th class="num">Rate</th><th class="num">Amount</th><th v-if="inv.status === 'draft'"></th></tr></thead>
        <tbody>
          <tr v-for="l in d.lines" :key="l.id">
            <td>{{ l.description }}</td>
            <td class="num">{{ l.kind === 'hours' ? `${hours(l.minutes)} h` : l.quantity }}</td>
            <td class="num">{{ money(l.unit_price_cents) }}{{ l.kind === 'hours' ? '/h' : '' }}</td>
            <td class="num">{{ money(l.amount_cents) }}</td>
            <td v-if="inv.status === 'draft'" class="actions"><button class="small ghost danger" @click="removeLine(l.id)">Remove</button></td>
          </tr>
        </tbody>
        <tfoot>
          <tr><td colspan="3">Total</td><td class="num">{{ money(inv.total_cents) }}</td><td v-if="inv.status === 'draft'"></td></tr>
        </tfoot>
      </table>

      <div v-if="inv.workers.length" style="margin-top: 16px">
        <div class="small muted">Man-hours</div>
        <div class="row">
          <span v-for="w in inv.workers" :key="w.worker_id" class="badge">{{ w.worker_name }}: {{ hours(w.minutes) }} h</span>
          <span class="badge accent">Total: {{ hours(inv.minutes) }} h</span>
        </div>
      </div>

      <label class="field" style="margin-top: 16px">
        Notes (printed on the invoice)
        <textarea :value="inv.notes" :disabled="inv.status === 'void'" @change="saveNotes(($event.target as HTMLTextAreaElement).value)"></textarea>
      </label>
    </div>

    <div v-if="d.entries.length" class="card flush">
      <div class="card-head">
        <h2>Time entries on this invoice</h2><span class="grow"></span>
        <button class="small" @click="showEntries = !showEntries">{{ showEntries ? 'Hide' : `Show ${d.entries.length}` }}</button>
      </div>
      <table v-if="showEntries">
        <thead><tr><th>Date</th><th>Worker</th><th>Project</th><th>Task</th><th class="num">Hours</th><th>Notes</th></tr></thead>
        <tbody>
          <tr v-for="e in d.entries" :key="e.id">
            <td class="nowrap">{{ dayLabel(e.work_date) }}</td>
            <td>{{ e.worker_name }}</td>
            <td>{{ e.project_name }}</td>
            <td>{{ e.task_title ?? '' }}</td>
            <td class="num">{{ hours(e.minutes) }}</td>
            <td class="small">{{ e.notes }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </template>

  <Modal v-if="sendForm" title="Email invoice" @close="sendForm = null">
    <div v-if="error" class="error">{{ error }}</div>
    <div v-if="settings && !settings.email_configured" class="error">
      Email isn't set up on the server. Download the PDF and send it from your own email, or configure SMTP (see Company &amp; email).
    </div>
    <form class="form-grid" @submit.prevent="send">
      <label class="field full">To <span class="hint">separate several with commas</span><input v-model="sendForm.to" required placeholder="client@example.com" /></label>
      <label class="field full">Cc<input v-model="sendForm.cc" /></label>
      <label class="field full">Subject<input v-model="sendForm.subject" /></label>
      <label class="field full">Message<textarea v-model="sendForm.message" rows="7"></textarea></label>
      <p class="full small muted" style="margin: 0">📎 invoice-{{ inv?.number }}.pdf is attached. {{ inv?.status === 'draft' ? 'The invoice will be marked sent.' : '' }}</p>
      <button type="submit" hidden></button>
    </form>
    <template #foot>
      <a class="btn" :href="pdfUrl" target="_blank" rel="noopener">Preview PDF</a>
      <span class="grow"></span>
      <button @click="sendForm = null">Cancel</button>
      <button class="primary" :disabled="busy || !settings?.email_configured" @click="send">{{ busy ? 'Sending…' : 'Send' }}</button>
    </template>
  </Modal>

  <Modal v-if="addingLine" title="Add line" @close="addingLine = false">
    <div v-if="error" class="error">{{ error }}</div>
    <form class="form-grid" @submit.prevent="addLine">
      <label class="field full">Description<input v-model="lineForm.description" required placeholder="e.g. Materials — drip edge" /></label>
      <label class="field">Quantity<input v-model="lineForm.quantity" inputmode="decimal" /></label>
      <label class="field">Unit price ($)<input v-model="lineForm.unit_price" inputmode="decimal" required /></label>
      <button type="submit" hidden></button>
    </form>
    <template #foot>
      <button @click="addingLine = false">Cancel</button>
      <button class="primary" :disabled="busy" @click="addLine">Add</button>
    </template>
  </Modal>
</template>
