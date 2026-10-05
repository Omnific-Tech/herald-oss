import { useState } from 'react'
import { Button } from '@/components/ui/button'

type CopyState = 'idle' | 'copied' | 'failed'

export function ProducerToken({
  name,
  token,
  onDismiss,
}: {
  name: string
  token: string
  onDismiss: () => void
}) {
  const [copyState, setCopyState] = useState<CopyState>('idle')

  async function copy() {
    try {
      await navigator.clipboard.writeText(token)
      setCopyState('copied')
    } catch {
      setCopyState('failed')
    }
  }

  return (
    <section
      aria-labelledby="producer-token-title"
      className="grid gap-3 rounded-xl border border-amber-500/50 bg-amber-50 p-4 text-sm dark:bg-amber-950/30"
    >
      <h2 id="producer-token-title" className="font-semibold">
        Copy the token for {name} now
      </h2>
      <p>
        This token is shown once and cannot be recovered. Herald stores only its hash. If it is
        lost, create a new producer and revoke this one.
      </p>
      {/* translate="no" keeps the token out of browser translation services. */}
      <code
        data-testid="producer-token"
        translate="no"
        className="rounded-md border bg-background px-3 py-2 font-mono break-all select-all"
      >
        {token}
      </code>
      {copyState === 'failed' && (
        <p className="text-destructive">Copy failed. Select the token and copy it manually.</p>
      )}
      <div className="flex flex-wrap gap-2">
        <Button size="sm" onClick={() => void copy()}>
          {copyState === 'copied' ? 'Copied' : 'Copy token'}
        </Button>
        <Button size="sm" variant="outline" onClick={onDismiss}>
          I have stored the token
        </Button>
      </div>
    </section>
  )
}
