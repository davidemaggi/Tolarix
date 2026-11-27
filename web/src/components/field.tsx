import { Label } from "@/components/ui/label";
import { cn } from "@/lib/utils";

type FieldProps = {
  id: string;
  label: React.ReactNode;
  hint?: React.ReactNode;
  error?: string;
  className?: string;
  /** Render the control; pass the aria props to it. */
  children: (aria: {
    id: string;
    "aria-invalid": boolean | undefined;
    "aria-describedby": string | undefined;
  }) => React.ReactNode;
};

/** A labelled form control with an optional hint and error message. */
export function Field({ id, label, hint, error, className, children }: FieldProps) {
  const hintId = hint ? `${id}-hint` : undefined;
  const errorId = error ? `${id}-error` : undefined;
  const describedBy = [hintId, errorId].filter(Boolean).join(" ") || undefined;
  return (
    <div className={cn("space-y-2", className)}>
      <Label htmlFor={id}>{label}</Label>
      {children({ id, "aria-invalid": error ? true : undefined, "aria-describedby": describedBy })}
      {hint && (
        <p id={hintId} className="text-sm text-muted-foreground">
          {hint}
        </p>
      )}
      {error && (
        <p id={errorId} className="text-sm text-destructive">
          {error}
        </p>
      )}
    </div>
  );
}

/** A form-level error, announced to screen readers. */
export function FormError({ children }: { children?: React.ReactNode }) {
  if (!children) return null;
  return (
    <p
      role="alert"
      className="rounded-md border border-destructive/40 bg-destructive/5 px-3 py-2 text-sm text-destructive"
    >
      {children}
    </p>
  );
}
