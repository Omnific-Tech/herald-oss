export { cn } from "cn"

const timestampFormat = new Intl.DateTimeFormat("en-GB", {
  dateStyle: "medium",
  timeStyle: "short",
  timeZone: "UTC",
})

export function formatTimestamp(value: string) {
  return `${timestampFormat.format(new Date(value))} UTC`
}
