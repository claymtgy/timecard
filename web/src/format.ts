// Display and input helpers. Money is integer cents, durations integer minutes,
// dates "YYYY-MM-DD" strings (never Date objects, to avoid time-zone shifts).

const usd = new Intl.NumberFormat('en-US', { style: 'currency', currency: 'USD' })

export function money(cents: number | null | undefined): string {
  return usd.format((cents ?? 0) / 100)
}

/** Decimal hours, e.g. 450 -> "7.5", 455 -> "7.58". */
export function hours(minutes: number | null | undefined): string {
  const h = (minutes ?? 0) / 60
  return (Math.round(h * 100) / 100).toString()
}

/** 450 -> "7:30 AM" */
export function clock(minute: number | null | undefined): string {
  if (minute == null) return ''
  const h24 = Math.floor(minute / 60) % 24
  const m = minute % 60
  const suffix = h24 < 12 ? 'AM' : 'PM'
  const h12 = h24 % 12 === 0 ? 12 : h24 % 12
  return `${h12}:${String(m).padStart(2, '0')} ${suffix}`
}

/** 450 -> "07:30" (value for <input type="time">) */
export function clockInput(minute: number | null | undefined): string {
  if (minute == null) return ''
  return `${String(Math.floor(minute / 60)).padStart(2, '0')}:${String(minute % 60).padStart(2, '0')}`
}

function parts(d: string): [number, number, number] {
  const [y, m, day] = d.split('-').map(Number)
  return [y, m, day]
}

function toUTC(d: string): Date {
  const [y, m, day] = parts(d)
  return new Date(Date.UTC(y, m - 1, day))
}

function fromUTC(dt: Date): string {
  return dt.toISOString().slice(0, 10)
}

export function today(): string {
  const now = new Date()
  return `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}-${String(now.getDate()).padStart(2, '0')}`
}

export function addDays(d: string, n: number): string {
  const dt = toUTC(d)
  dt.setUTCDate(dt.getUTCDate() + n)
  return fromUTC(dt)
}

/** Monday on or before d. */
export function weekStart(d: string): string {
  const dow = toUTC(d).getUTCDay() // 0=Sun
  return addDays(d, -((dow + 6) % 7))
}

const dayFmt = new Intl.DateTimeFormat('en-US', { weekday: 'short', month: 'short', day: 'numeric', timeZone: 'UTC' })
const longDayFmt = new Intl.DateTimeFormat('en-US', { weekday: 'long', timeZone: 'UTC' })
const dateFmt = new Intl.DateTimeFormat('en-US', { month: 'short', day: 'numeric', year: 'numeric', timeZone: 'UTC' })
const shortFmt = new Intl.DateTimeFormat('en-US', { month: 'numeric', day: 'numeric', timeZone: 'UTC' })

/** "Mon, Apr 27" */
export function dayLabel(d: string): string {
  return dayFmt.format(toUTC(d))
}
/** "Monday" */
export function weekday(d: string): string {
  return longDayFmt.format(toUTC(d))
}
/** "Apr 27, 2026" */
export function dateLabel(d: string | null | undefined): string {
  return d ? dateFmt.format(toUTC(d)) : ''
}
/** "4/27 – 5/3" (the spreadsheet's date-range style) */
export function rangeLabel(a: string | null | undefined, b: string | null | undefined): string {
  if (!a && !b) return ''
  if (!b || a === b) return shortFmt.format(toUTC((a ?? b)!))
  if (!a) return `– ${shortFmt.format(toUTC(b))}`
  return `${shortFmt.format(toUTC(a))} – ${shortFmt.format(toUTC(b))}`
}

/** "$1,234.50" / "1234.5" -> 123450; blank -> 0 */
export function dollarsToCents(v: string | number | null | undefined): number {
  if (v === null || v === undefined || v === '') return 0
  const n = typeof v === 'number' ? v : Number(String(v).replace(/[$,\s]/g, ''))
  return Number.isFinite(n) ? Math.round(n * 100) : 0
}

export function centsToDollars(c: number | null | undefined): string {
  if (c === null || c === undefined) return ''
  return (c / 100).toFixed(2).replace(/\.00$/, '')
}

export function percent(bps: number | null | undefined): string {
  return `${((bps ?? 0) / 100).toString()}%`
}

export const statusLabels: Record<string, string> = {
  active: 'Active',
  on_hold: 'On hold',
  completed: 'Completed',
  archived: 'Archived',
  planned: 'Planned',
  done: 'Done',
  cancelled: 'Cancelled',
  todo: 'To do',
  in_progress: 'In progress',
  blocked: 'Blocked',
  draft: 'Draft',
  sent: 'Sent',
  paid: 'Paid',
  void: 'Void',
  hourly: 'Hourly',
  fixed: 'Fixed bid',
}

export function label(s: string | null | undefined): string {
  return s ? (statusLabels[s] ?? s) : ''
}
