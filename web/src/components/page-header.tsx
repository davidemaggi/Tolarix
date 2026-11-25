export function PageHeader({ title, intro }: { title: string; intro?: string }) {
  return (
    <div className="mb-8 space-y-2">
      <h1 className="text-2xl font-semibold tracking-tight">{title}</h1>
      {intro && <p className="max-w-prose text-muted-foreground">{intro}</p>}
    </div>
  );
}
