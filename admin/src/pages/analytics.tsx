import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'

const metrics = ['Messages published', 'Deliveries', 'Acknowledgements', 'Failed deliveries']

export function AnalyticsPage() {
  return (
    <div className="grid gap-6">
      <div>
        <h1 className="text-xl font-semibold">Analytics</h1>
        <p className="text-sm text-muted-foreground">
          No analytics endpoint yet. These panels fill in once the admin API exposes delivery metrics.
        </p>
      </div>
      <div className="grid gap-4 sm:grid-cols-2">
        {metrics.map((metric) => (
          <Card key={metric}>
            <CardHeader>
              <CardTitle>{metric}</CardTitle>
            </CardHeader>
            <CardContent>
              <p className="text-2xl font-semibold text-muted-foreground">—</p>
            </CardContent>
          </Card>
        ))}
      </div>
    </div>
  )
}
