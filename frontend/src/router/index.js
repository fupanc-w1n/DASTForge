import { createRouter, createWebHashHistory } from 'vue-router'

const routes = [
  { path: '/login', component: () => import('@/views/Login.vue'), meta: { public: true } },
  {
    path: '/',
    component: () => import('@/components/layout/AppShell.vue'),
    redirect: '/dashboard',
    children: [
      { path: 'dashboard', component: () => import('@/views/Dashboard.vue') },
      { path: 'policies', component: () => import('@/views/PolicyList.vue') },
      { path: 'policies/new', component: () => import('@/views/PolicyEdit.vue') },
      { path: 'policies/:id', component: () => import('@/views/PolicyEdit.vue'), props: true },
      { path: 'tasks', component: () => import('@/views/TaskList.vue') },
      { path: 'tasks/new', component: () => import('@/views/TaskNew.vue') },
      { path: 'tasks/:id', component: () => import('@/views/TaskDetail.vue'), props: true },
      { path: 'resources', component: () => import('@/views/Resources.vue') }
    ]
  }
]

const router = createRouter({ history: createWebHashHistory(), routes })

router.beforeEach((to) => {
  if (to.meta?.public) return true
  const token = localStorage.getItem('dast_token')
  if (!token) return '/login'
  return true
})

export default router
