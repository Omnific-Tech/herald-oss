import { infiniteQueryOptions, queryOptions } from '@tanstack/react-query'
import { api } from './api'

export const sessionQuery = queryOptions({
  queryKey: ['session'],
  queryFn: api.session,
  // Expiry is detected by the 401 on the next admin request.
  staleTime: Infinity,
  meta: { authRedirect: false },
})

export const healthQuery = queryOptions({
  queryKey: ['health'],
  queryFn: api.health,
})

export const applicationsQuery = queryOptions({
  queryKey: ['applications'],
  queryFn: api.applications,
})

export const producersQuery = (slug: string) =>
  queryOptions({
    queryKey: ['applications', slug, 'producers'],
    queryFn: () => api.producers(slug),
  })

export const routingQuery = (slug: string) =>
  queryOptions({
    queryKey: ['applications', slug, 'routing'],
    queryFn: () => api.routing(slug),
  })

export const historyQuery = (slug: string) =>
  infiniteQueryOptions({
    queryKey: ['applications', slug, 'messages'],
    queryFn: ({ pageParam }) => api.messages(slug, pageParam),
    initialPageParam: null as string | null,
    getNextPageParam: (page) => page.next_cursor,
  })
