<script setup lang="ts">
import { ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import Login from './views/Login.vue'
import { isAdmin, isManager, logout, session } from './session'

// On phones the navigation collapses behind a Menu button so the clock is on screen.
const menuOpen = ref(false)
const route = useRoute()
watch(() => route.path, () => (menuOpen.value = false))
</script>

<template>
  <Login v-if="session.anonymous" />
  <div v-else-if="session.error" class="main"><div class="error">Could not load the app: {{ session.error }}</div></div>
  <div v-else-if="!session.loaded" class="main muted">Loading…</div>
  <div v-else class="shell">
    <nav class="sidebar" :class="{ open: menuOpen }">
      <div class="brand">
        <span class="brand-mark"></span> Crew Timesheets
        <button class="menu-toggle small" :aria-expanded="menuOpen" @click="menuOpen = !menuOpen">{{ menuOpen ? 'Close' : 'Menu' }}</button>
      </div>
      <RouterLink class="nav-link" to="/" exact-active-class="router-link-active" active-class="">Clock in</RouterLink>
      <RouterLink class="nav-link" to="/timesheet">My time</RouterLink>
      <RouterLink class="nav-link" to="/tasks">Tasks</RouterLink>
      <RouterLink class="nav-link" to="/projects">Projects</RouterLink>
      <template v-if="isManager">
        <div class="nav-section">Office</div>
        <RouterLink class="nav-link" to="/dashboard">Dashboard</RouterLink>
        <RouterLink class="nav-link" to="/time-log">Time log</RouterLink>
        <RouterLink class="nav-link" to="/invoices">Invoices</RouterLink>
        <RouterLink class="nav-link" to="/reports/projects">Project profit</RouterLink>
        <RouterLink class="nav-link" to="/payroll">Payroll</RouterLink>
        <RouterLink class="nav-link" to="/clients">Clients</RouterLink>
        <div class="nav-section">Setup</div>
        <RouterLink class="nav-link" to="/templates">Task templates</RouterLink>
        <RouterLink class="nav-link" to="/workers">Workers</RouterLink>
        <RouterLink v-if="isAdmin" class="nav-link" to="/users">Users</RouterLink>
        <RouterLink class="nav-link" to="/settings">Company &amp; email</RouterLink>
      </template>
      <div class="sidebar-foot">
        <div class="small">{{ session.me?.name || session.me?.email }}</div>
        <div class="small muted" style="margin-bottom: 8px">{{ session.me?.role }}</div>
        <button class="small" @click="logout">Sign out</button>
      </div>
    </nav>
    <main class="main">
      <RouterView :key="$route.path" />
    </main>
  </div>
</template>
