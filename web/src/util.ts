import { ref } from 'vue'

/** Wraps async UI actions with a busy flag and a user-facing error message. */
export function useAction() {
  const error = ref('')
  const busy = ref(false)
  async function run<T>(fn: () => Promise<T>): Promise<T | undefined> {
    error.value = ''
    busy.value = true
    try {
      return await fn()
    } catch (e) {
      error.value = (e as Error).message
      return undefined
    } finally {
      busy.value = false
    }
  }
  return { error, busy, run }
}

/** Download rows as a CSV file (client-side export for spreadsheets). */
export function downloadCsv(filename: string, header: string[], rows: (string | number | null | undefined)[][]) {
  const esc = (v: string | number | null | undefined) => {
    const s = v === null || v === undefined ? '' : String(v)
    return /[",\n]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s
  }
  const body = [header, ...rows].map((r) => r.map(esc).join(',')).join('\r\n')
  const url = URL.createObjectURL(new Blob([body], { type: 'text/csv' }))
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  a.click()
  URL.revokeObjectURL(url)
}

export function confirmDelete(what: string): boolean {
  return window.confirm(`Delete ${what}? This can't be undone.`)
}
