import { createRouter, createWebHistory } from 'vue-router'
const allRoutes = [
  {
    path: '/',
    name: 'Home',
    component: () => import('../views/home/index.vue')
  }
]
const router = createRouter({
  history: createWebHistory(),
  routes: allRoutes,
  scrollBehavior() {
    return {
      el: '#app',
      top: 0,
      behavior: 'smooth',
    };
  },
});

export default router;