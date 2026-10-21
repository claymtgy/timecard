<script setup lang="ts">
import { loginUrl } from '../api'

// Shown when nobody is signed in. Credentials are entered on the sign-in service's
// page (OIDC), so passwords never touch this app.
const target = loginUrl(location.pathname === '/' ? '/' : location.pathname + location.search)
</script>

<template>
  <div class="login-wrap">
    <div class="login-card">
      <div class="brand"><span class="brand-mark"></span> Crew Timesheets</div>
      <h1>Welcome</h1>
      <p class="muted">Sign in to clock in, track your time and see your tasks.</p>
      <a class="btn primary big" :href="target">Sign in</a>
      <p class="small muted" style="margin-top: 18px">New here? Choose <strong>Create one</strong> on the next screen, or ask your admin for an account.</p>
    </div>
  </div>
</template>

<style scoped>
.login-wrap { min-height: 100vh; display: grid; place-items: center; padding: 16px; }
.login-card { width: 100%; max-width: 380px; background: var(--surface); border: 1px solid var(--line); border-radius: 14px; padding: 28px; }
.brand { padding: 0 0 18px; }
h1 { margin-bottom: 4px; }
.big { width: 100%; justify-content: center; padding: 12px; font-size: 15px; margin-top: 8px; }
</style>
