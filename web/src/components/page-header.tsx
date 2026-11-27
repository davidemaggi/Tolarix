export function PageHeader({
  title,
  intro,
  actions,
  before,
}: {
  title: React.ReactNode;
  intro?: React.ReactNode;
  actions?: React.ReactNode;
  /** Shown above the title, e.g. a back link. */
  before?: React.ReactNode;
}) {
  return (
    <div className="mb-8 space-y-3">
      {before}
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div className="min-w-0 space-y-2">
          <h1 className="text-2xl font-semibold tracking-tight break-words">{title}</h1>
          {intro && <p className="max-w-prose text-muted-foreground">{intro}</p>}
        </div>
        {actions && <div className="flex flex-wrap gap-2">{actions}</div>}
      </div>
    </div>
  );
}
