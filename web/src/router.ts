import { createRouter, createWebHistory } from 'vue-router'
import { ensureSession, isAdmin, isManager, session } from './session'

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', component: () => import('./views/Clock.vue') },
    { path: '/dashboard', component: () => import('./views/Dashboard.vue') },
    { path: '/timesheet', component: () => import('./views/Timesheet.vue') },
    { path: '/time-log', component: () => import('./views/TimeLog.vue'), meta: { manager: true } },
    { path: '/tasks', component: () => import('./views/Tasks.vue') },
    { path: '/projects', component: () => import('./views/Projects.vue') },
    { path: '/projects/:id', component: () => import('./views/ProjectDetail.vue'), props: true },
    { path: '/templates', component: () => import('./views/Templates.vue'), meta: { manager: true } },
    { path: '/reports/projects', component: () => import('./views/ProjectReports.vue'), meta: { manager: true } },
    { path: '/invoices', component: () => import('./views/Invoices.vue'), meta: { manager: true } },
    { path: '/invoices/:id', component: () => import('./views/InvoiceDetail.vue'), props: true, meta: { manager: true } },
    { path: '/payroll', component: () => import('./views/Payroll.vue'), meta: { manager: true } },
    { path: '/payroll/runs/:id', component: () => import('./views/PayRunDetail.vue'), props: true, meta: { manager: true } },
    { path: '/clients', component: () => import('./views/Clients.vue'), meta: { manager: true } },
    { path: '/clients/:id', component: () => import('./views/ClientDetail.vue'), props: true, meta: { manager: true } },
    { path: '/workers', component: () => import('./views/Workers.vue'), meta: { manager: true } },
    { path: '/users', component: () => import('./views/Users.vue'), meta: { admin: true } },
    { path: '/settings', component: () => import('./views/Settings.vue'), meta: { manager: true } },
    { path: '/:pathMatch(.*)*', component: () => import('./views/NotFound.vue') },
  ],
})

router.beforeEach(async (to) => {
  await ensureSession()
  if (!session.loaded) return true // App shows the login screen or the load error
  if (to.meta.admin && !isAdmin.value) return '/'
  if (to.meta.manager && !isManager.value) return '/'
})
