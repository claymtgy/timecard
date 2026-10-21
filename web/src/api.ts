// Thin fetch wrapper for the Go API. Cookies carry the session; writes send the CSRF token.

let csrfToken = ''
export function setCsrfToken(t: string) {
  csrfToken = t
}

let onUnauthorized: () => void = () => {}
/** Called when the API says the session is missing/expired (the app then shows the login screen). */
export function setUnauthorizedHandler(fn: () => void) {
  onUnauthorized = fn
}

export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message)
  }
}

export function loginUrl(returnTo = location.pathname + location.search) {
  return '/auth/login?return_to=' + encodeURIComponent(returnTo)
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json' }
  if (body !== undefined) headers['Content-Type'] = 'application/json'
  if (method !== 'GET') headers['X-CSRF-Token'] = csrfToken
  const res = await fetch(path, {
    method,
    headers,
    credentials: 'same-origin',
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (res.status === 401) {
    onUnauthorized()
    throw new ApiError(401, 'Sign in required')
  }
  if (res.status === 204) return undefined as T
  const data = await res.json().catch(() => ({}))
  if (!res.ok) throw new ApiError(res.status, (data as { error?: string }).error || res.statusText)
  return data as T
}

export const api = {
  get: <T>(path: string, params?: Record<string, string | number | boolean | null | undefined>) => {
    const q = new URLSearchParams()
    for (const [k, v] of Object.entries(params ?? {})) {
      if (v !== undefined && v !== null && v !== '' && v !== false) q.set(k, v === true ? '1' : String(v))
    }
    const qs = q.toString()
    return request<T>('GET', qs ? `${path}?${qs}` : path)
  },
  post: <T>(path: string, body?: unknown) => request<T>('POST', path, body ?? {}),
  put: <T>(path: string, body: unknown) => request<T>('PUT', path, body),
  patch: <T>(path: string, body: unknown) => request<T>('PATCH', path, body),
  del: (path: string) => request<void>('DELETE', path),
}

// ---- API types (mirror internal/app JSON) ----

export type Role = 'admin' | 'manager' | 'worker'

export interface Me {
  user: { id: number; email: string; name: string; role: Role; worker_id: number | null }
  csrf_token: string
}

export interface Worker {
  id: number
  name: string
  email: string
  phone: string
  pay_rate_cents: number
  active: boolean
  notes: string
  user_id: number | null
  user_email: string | null
}

export interface Client {
  id: number
  name: string
  contact_name: string
  email: string
  phone: string
  address: string
  notes: string
  active: boolean
  project_count: number
  outstanding_cents: number
}

export type BillingType = 'hourly' | 'fixed'
export type ProjectStatus = 'planned' | 'active' | 'done' | 'cancelled'
export interface Project {
  id: number
  client_id: number | null
  client_name: string | null
  name: string
  description: string
  billing_type: BillingType
  bill_rate_cents?: number
  bid_amount_cents?: number
  labor_cost_rate_cents?: number
  pay_rate_override_cents?: number | null
  profit_share_bps?: number
  profit_share_worker_id?: number | null
  status: ProjectStatus
  start_date: string | null
  completed_on: string | null
  minutes: number
  unbilled_minutes: number
  task_count: number
  open_tasks: number
  invoiced_cents?: number
}

export type TaskStatus = 'todo' | 'in_progress' | 'blocked' | 'done'
export interface Task {
  id: number
  project_id: number
  project_name: string
  title: string
  description: string
  status: TaskStatus
  assignee_worker_id: number | null
  assignee_name: string | null
  due_date: string | null
  estimate_minutes: number | null
  position: number
  minutes: number
}

export interface TaskTemplateItem {
  id?: number
  title: string
  description: string
  estimate_minutes: number | null
}
export interface TaskTemplate {
  id: number
  name: string
  description: string
  items: TaskTemplateItem[]
}

export interface TimeEntry {
  id: number
  worker_id: number
  worker_name: string
  project_id: number
  project_name: string
  client_name: string | null
  billing_type: BillingType
  task_id: number | null
  task_title: string | null
  work_date: string
  start_minute: number | null
  end_minute: number | null
  break_minutes: number
  minutes: number
  pay_rate_cents: number
  billable: boolean
  notes: string
  invoice_id: number | null
  invoice_number: number | null
  pay_run_id: number | null
  pay_run_status: string | null
}

export interface TimeEntryInput {
  worker_id: number
  project_id: number
  task_id: number | null
  work_date: string
  start: string
  end: string
  break_minutes: number
  minutes: number | null
  billable: boolean
  notes: string
  pay_rate_cents?: number | null
}

export interface ClockSession {
  worker_id: number
  worker_name: string
  project_id: number
  project_name: string
  client_name: string | null
  task_id: number | null
  task_title: string | null
  started_at: string
  notes: string
}

export interface ClockState {
  linked: boolean
  worker_id?: number
  server_time: string
  session?: ClockSession | null
  last?: { project_id: number; task_id: number | null; break_minutes: number } | null
}

export interface WorkerMinutes {
  worker_id: number
  worker_name: string
  minutes: number
  pay_cents: number
}

export type InvoiceStatus = 'draft' | 'sent' | 'paid' | 'void'
export interface Invoice {
  id: number
  number: number
  client_id: number | null
  client_name: string | null
  client_email: string | null
  period_start: string | null
  period_end: string | null
  issue_date: string
  due_date: string | null
  status: InvoiceStatus
  paid_date: string | null
  sent_at: string | null
  sent_to: string
  notes: string
  total_cents: number
  minutes: number
  projects: string
  workers: WorkerMinutes[]
}

export interface InvoiceLine {
  id: number
  project_id: number | null
  project_name: string | null
  kind: 'hours' | 'fixed' | 'other'
  description: string
  minutes: number | null
  quantity: number
  unit_price_cents: number
  amount_cents: number
}

export interface InvoiceDetail {
  invoice: Invoice
  lines: InvoiceLine[]
  entries: TimeEntry[]
}

export interface UnbilledProject {
  project_id: number
  project_name: string
  client_id: number | null
  billing_type: BillingType
  status: ProjectStatus
  bill_rate_cents: number
  minutes: number
  first_date: string | null
  last_date: string | null
  amount_cents: number
  bid_amount_cents: number
  invoiced_cents: number
}

export interface CompanySettings {
  name: string
  address: string
  phone: string
  email: string
  invoice_footer: string
  default_due_days: number
}
export interface Settings {
  company: CompanySettings
  email_configured: boolean
  email_from: string
}

export interface PayRun {
  id: number
  worker_id: number
  worker_name: string
  period_start: string
  period_end: string
  status: 'draft' | 'paid'
  paid_date: string | null
  notes: string
  minutes: number
  wages_cents: number
  adjustments_cents: number
  total_cents: number
}

export interface Adjustment {
  id: number
  worker_id: number
  worker_name: string
  project_id: number | null
  project_name: string | null
  kind: 'profit_share' | 'bonus' | 'reimbursement' | 'deduction' | 'other'
  description: string
  amount_cents: number
  adj_date: string
  pay_run_id: number | null
}

export interface PayRunDetail {
  pay_run: PayRun
  entries: TimeEntry[]
  adjustments: Adjustment[]
}

export interface UnpaidWorker {
  worker_id: number
  worker_name: string
  pay_rate_cents: number
  minutes: number
  wages_cents: number
  adjustments_cents: number
  total_cents: number
  first_date: string | null
  last_date: string | null
}

export interface PhaseMinutes {
  task_id: number | null
  title: string
  minutes: number
  workers: WorkerMinutes[]
}

export interface ProjectReport {
  project_id: number
  project_name: string
  client_id: number | null
  client_name: string | null
  billing_type: BillingType
  status: ProjectStatus
  completed_on: string | null
  bid_amount_cents: number
  bill_rate_cents: number
  labor_cost_rate_cents: number
  profit_share_bps: number
  profit_share_worker_id: number | null
  profit_share_worker_name: string | null
  invoiced_cents: number
  paid_cents: number
  posted_share_cents: number | null
  posted_share_paid: boolean
  minutes: number
  billable_minutes: number
  wages_cents: number
  workers: WorkerMinutes[]
  phases: PhaseMinutes[]
  revenue_cents: number
  labor_cost_cents: number
  profit_cents: number
  profit_share_cents: number
  share_worker_pay_cents: number
}

export interface AppUser {
  id: number
  email: string
  name: string
  role: Role
  worker_id: number | null
  worker_name: string | null
  last_login_at: string | null
}

export interface Dashboard {
  week_start: string
  week_end: string
  unlinked?: boolean
  week?: { worker_id: number; worker_name: string; minutes: number }[]
  tasks?: Task[]
  unbilled_minutes?: number
  unbilled_cents?: number
  outstanding_cents?: number
  open_projects?: number
  unpaid_payroll_cents?: number
  unpaid?: UnpaidWorker[]
  clocked_in?: ClockSession[]
}
