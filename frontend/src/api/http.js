import axios from 'axios'
import router from '@/router'
import { ElMessage } from 'element-plus'

const http = axios.create({
  baseURL: '/api/v1',
  timeout: 15000
})

http.interceptors.request.use((cfg) => {
  const token = localStorage.getItem('dast_token')
  if (token) cfg.headers.Authorization = `Bearer ${token}`
  return cfg
})

http.interceptors.response.use(
  (resp) => resp.data,
  (err) => {
    const status = err.response?.status
    const msg = err.response?.data?.error || err.message
    if (status === 401) {
      localStorage.removeItem('dast_token')
      if (router.currentRoute.value.path !== '/login') {
        router.replace('/login')
      }
    } else if (status === 409) {
      ElMessage.warning(msg)
    } else if (status >= 500) {
      ElMessage.error(msg || '服务器错误')
    } else if (msg) {
      ElMessage.error(msg)
    }
    return Promise.reject(err)
  }
)

export default http
