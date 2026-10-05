import { Link } from '@tanstack/react-router'

export function NotFoundPage() {
  return (
    <main className="mx-auto max-w-md p-8 text-center">
      <h1 className="text-lg font-semibold">Page not found</h1>
      <Link to="/" className="mt-4 inline-block text-sm underline">
        Back to overview
      </Link>
    </main>
  )
}
