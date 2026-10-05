import type { ComponentProps, ReactElement, ReactNode } from 'react'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

type FieldProps = {
  id: string
  label: string
  error?: string
  hint?: ReactNode
  aside?: ReactNode
}

type ControlProps = {
  id: string
  'aria-invalid'?: true
  'aria-describedby'?: string
}

export function FieldLayout({
  id,
  label,
  error,
  hint,
  aside,
  children,
}: FieldProps & { children: (control: ControlProps) => ReactElement }) {
  const describedBy = error ? `${id}-error` : hint ? `${id}-hint` : undefined
  return (
    <div className="grid gap-1.5">
      <div className="flex items-baseline justify-between gap-2">
        <Label htmlFor={id}>{label}</Label>
        {aside}
      </div>
      {children({ id, 'aria-invalid': error ? true : undefined, 'aria-describedby': describedBy })}
      {error ? (
        <p id={`${id}-error`} className="text-sm text-destructive">
          {error}
        </p>
      ) : hint ? (
        <p id={`${id}-hint`} className="text-sm text-muted-foreground">
          {hint}
        </p>
      ) : null}
    </div>
  )
}

export function Field({ id, label, error, hint, aside, ...input }: FieldProps & ComponentProps<typeof Input>) {
  return (
    <FieldLayout id={id} label={label} error={error} hint={hint} aside={aside}>
      {(control) => <Input {...control} {...input} />}
    </FieldLayout>
  )
}
