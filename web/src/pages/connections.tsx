import { Pencil, Plus, Trash2 } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { toast } from "sonner";

import type { Connection } from "@/api/client";
import { useConnections, useConnectorKinds, useDeleteConnection } from "@/api/queries";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { PageHeader } from "@/components/page-header";
import { QueryError } from "@/components/query-state";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { errorMessage } from "@/lib/errors";

export function ConnectionsPage() {
  const { t } = useTranslation();
  const connections = useConnections();
  const kinds = useConnectorKinds();
  const del = useDeleteConnection();
  const [deleting, setDeleting] = useState<Connection | null>(null);
  const kindName = (k: string) => kinds.data?.find((x) => x.kind === k)?.displayName ?? k;

  return (
    <>
      <PageHeader
        title={t("connections.title")}
        intro={t("connections.intro")}
        actions={
          <Button asChild>
            <Link to="/connections/new">
              <Plus aria-hidden="true" />
              {t("connections.new")}
            </Link>
          </Button>
        }
      />
      {connections.isPending ? (
        <Skeleton className="h-32" />
      ) : connections.isError ? (
        <QueryError error={connections.error} onRetry={() => void connections.refetch()} />
      ) : connections.data.length === 0 ? (
        <p className="rounded-xl border border-dashed px-6 py-10 text-center text-muted-foreground">
          {t("connections.empty")}
        </p>
      ) : (
        <div className="rounded-xl border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("common.name")}</TableHead>
                <TableHead>{t("connections.kind")}</TableHead>
                <TableHead>{t("connections.usedBy")}</TableHead>
                <TableHead className="w-0">
                  <span className="sr-only">{t("common.actions")}</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {connections.data.map((c) => (
                <TableRow key={c.id}>
                  <TableCell className="font-medium">
                    <Link to={`/connections/${c.id}`} className="hover:underline">
                      {c.name}
                    </Link>
                  </TableCell>
                  <TableCell>{kindName(c.kind)}</TableCell>
                  <TableCell className="text-muted-foreground">
                    {c.usedBy.length
                      ? c.usedBy.map((f, i) => (
                          <span key={f.id}>
                            {i > 0 && ", "}
                            <Link to={`/flows/${f.id}`} className="hover:underline">
                              {f.name}
                            </Link>
                          </span>
                        ))
                      : t("connections.unused")}
                  </TableCell>
                  <TableCell>
                    <div className="flex justify-end gap-1">
                      <Button variant="ghost" size="icon" asChild>
                        <Link
                          to={`/connections/${c.id}`}
                          aria-label={t("common.editNamed", { name: c.name })}
                        >
                          <Pencil aria-hidden="true" />
                        </Link>
                      </Button>
                      <Button
                        variant="ghost"
                        size="icon"
                        onClick={() => setDeleting(c)}
                        aria-label={t("common.deleteNamed", { name: c.name })}
                      >
                        <Trash2 aria-hidden="true" />
                      </Button>
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
      <ConfirmDialog
        open={!!deleting}
        onOpenChange={(o) => !o && setDeleting(null)}
        title={t("connections.deleteTitle", { name: deleting?.name ?? "" })}
        description={
          deleting?.usedBy.length
            ? t("connections.deleteInUse", {
                flows: deleting.usedBy.map((f) => f.name).join(", "),
              })
            : t("connections.deleteBody")
        }
        confirmLabel={t("common.delete")}
        destructive
        disabled={!!deleting?.usedBy.length || del.isPending}
        onConfirm={() => {
          if (!deleting) return;
          del.mutate(deleting.id, {
            onSuccess: () => {
              toast.success(t("connections.deleted", { name: deleting.name }));
              setDeleting(null);
            },
            onError: (err) => toast.error(errorMessage(err, t)),
          });
        }}
      />
    </>
  );
}
