<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api, type Dashboard } from '../api'
import { dateLabel, hours, label, money, rangeLabel } from '../format'
import { isManager, session } from '../session'

const d = ref<Dashboard | null>(null)
const error = ref('')
onMounted(async () => {
  try {
    d.value = await api.get<Dashboard>('/api/reports/dashboard')
  } catch (e) {
    error.value = (e as Error).message
  }
})
</script>

<template>
  <div class="page-head">
    <h1>Hi {{ session.me?.name?.split(' ')[0] || 'there' }}</h1>
    <span class="spacer"></span>
    <RouterLink class="btn primary" to="/">Clock in</RouterLink>
  </div>
  <div v-if="error" class="error">{{ error }}</div>
  <template v-if="d">
    <div v-if="d.unlinked" class="card empty">
      Your sign-in isn't linked to a worker yet. Ask an admin to link your account on the Users page.
    </div>

    <div v-if="isManager" class="stats">
      <RouterLink to="/invoices" class="stat">
        <div class="k">Unbilled hourly work</div>
        <div class="v">{{ money(d.unbilled_cents) }}</div>
        <div class="small muted">{{ hours(d.unbilled_minutes) }} h</div>
      </RouterLink>
      <RouterLink to="/invoices" class="stat">
        <div class="k">Invoices awaiting payment</div>
        <div class="v">{{ money(d.outstanding_cents) }}</div>
      </RouterLink>
      <RouterLink to="/payroll" class="stat">
        <div class="k">Unpaid payroll</div>
        <div class="v">{{ money(d.unpaid_payroll_cents) }}</div>
      </RouterLink>
      <RouterLink to="/projects" class="stat">
        <div class="k">Open projects</div>
        <div class="v">{{ d.open_projects }}</div>
      </RouterLink>
    </div>

    <div class="grid cols-2">
      <div v-if="isManager" class="card flush">
        <div class="card-head"><h2>Clocked in now</h2><span class="muted small">{{ d.clocked_in?.length ?? 0 }}</span></div>
        <table>
          <tbody>
            <tr v-for="c in d.clocked_in" :key="c.worker_id">
              <td><strong>{{ c.worker_name }}</strong></td>
              <td>{{ c.project_name }}<div class="small muted">{{ c.task_title ?? '' }}</div></td>
              <td class="nowrap small">since {{ new Date(c.started_at).toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' }) }}</td>
            </tr>
          </tbody>
        </table>
        <div v-if="!d.clocked_in?.length" class="empty">Nobody is on the clock.</div>
      </div>
      <div class="card flush">
        <div class="card-head"><h2>This week</h2><span class="muted small">{{ rangeLabel(d.week_start, d.week_end) }}</span></div>
        <table>
          <tbody>
            <tr v-for="w in d.week" :key="w.worker_id">
              <td>
                <RouterLink :to="{ path: '/timesheet', query: { worker: w.worker_id, week: d.week_start } }">{{ w.worker_name }}</RouterLink>
              </td>
              <td class="num">{{ hours(w.minutes) }} h</td>
            </tr>
          </tbody>
        </table>
        <div v-if="!d.week?.length" class="empty">No time logged yet this week.</div>
      </div>

      <div class="card flush">
        <div class="card-head"><h2>{{ isManager ? 'Tasks due in the next week' : 'My open tasks' }}</h2></div>
        <table>
          <tbody>
            <tr v-for="t in d.tasks" :key="t.id">
              <td>
                <RouterLink :to="`/projects/${t.project_id}`">{{ t.title }}</RouterLink>
                <div class="small muted">{{ t.project_name }}</div>
              </td>
              <td><span class="badge" :class="t.status">{{ label(t.status) }}</span></td>
              <td class="nowrap small">{{ dateLabel(t.due_date) }}</td>
            </tr>
          </tbody>
        </table>
        <div v-if="!d.tasks?.length" class="empty">Nothing here.</div>
      </div>

      <div v-if="isManager && d.unpaid?.length" class="card flush">
        <div class="card-head"><h2>Owed to workers</h2><span class="spacer grow"></span><RouterLink to="/payroll">Payroll →</RouterLink></div>
        <table>
          <thead><tr><th>Worker</th><th>Dates</th><th class="num">Hours</th><th class="num">Owed</th></tr></thead>
          <tbody>
            <tr v-for="u in d.unpaid" :key="u.worker_id">
              <td>{{ u.worker_name }}</td>
              <td class="small">{{ rangeLabel(u.first_date, u.last_date) }}</td>
              <td class="num">{{ hours(u.minutes) }}</td>
              <td class="num">{{ money(u.total_cents) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>
  </template>
</template>

<style scoped>
a.stat { color: inherit; text-decoration: none; }
a.stat:hover { border-color: var(--accent); }
</style>
