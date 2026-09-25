export type InstantValue = string | number | Date;

type LocalDateTimeOptions = Omit<Intl.DateTimeFormatOptions, "timeZone">;

const defaultDateTimeOptions: LocalDateTimeOptions = {
  year: "numeric",
  month: "numeric",
  day: "numeric",
  hour: "numeric",
  minute: "2-digit",
  second: "2-digit",
};

const dateTimeFormatter = new Intl.DateTimeFormat(undefined, {
  dateStyle: "medium",
  timeStyle: "medium",
});
const dateFormatter = new Intl.DateTimeFormat(undefined, { dateStyle: "medium" });
const timeFormatter = new Intl.DateTimeFormat(undefined, { timeStyle: "medium" });

function parseInstant(value: InstantValue): Date | undefined {
  const parsed = value instanceof Date ? value : new Date(value);
  return Number.isNaN(parsed.getTime()) ? undefined : parsed;
}

function fallback(value: InstantValue): string {
  return value instanceof Date ? value.toISOString() : String(value);
}

// Deliberately omit timeZone: Intl then uses the user's real browser/OS zone
// and locale-specific date ordering, clock style, and zone naming.
export function formatLocalDateTime(value: InstantValue, options?: LocalDateTimeOptions): string {
  const parsed = parseInstant(value);
  if (!parsed) return fallback(value);
  return options ? new Intl.DateTimeFormat(undefined, { ...defaultDateTimeOptions, ...options }).format(parsed) : dateTimeFormatter.format(parsed);
}

export function formatLocalDate(value: InstantValue, options?: LocalDateTimeOptions): string {
  const parsed = parseInstant(value);
  if (!parsed) return fallback(value);
  return options ? new Intl.DateTimeFormat(undefined, options).format(parsed) : dateFormatter.format(parsed);
}

export function formatLocalTime(value: InstantValue): string {
  const parsed = parseInstant(value);
  return parsed ? timeFormatter.format(parsed) : fallback(value);
}

export function browserTimeZone(): string {
  return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
}
