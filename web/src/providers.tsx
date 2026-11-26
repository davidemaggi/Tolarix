import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useEffect, useState } from "react";

import i18n from "@/i18n";

import { ApiError } from "@/api/client";
import { ThemeProvider } from "@/components/theme-provider";
import { Toaster } from "@/components/ui/sonner";

export function AppProviders({ children }: { children: React.ReactNode }) {
  const [queryClient] = useState(
    () =>
      new QueryClient({
        defaultOptions: {
          queries: {
            staleTime: 10_000,
            // Retrying a 4xx answer cannot help.
            retry: (count, err) =>
              !(err instanceof ApiError && err.status >= 400 && err.status < 500) && count < 2,
          },
        },
      }),
  );
  useEffect(() => {
    // Flows carry a schedule sentence in the UI language.
    const refresh = () => void queryClient.invalidateQueries({ queryKey: ["flows"] });
    i18n.on("languageChanged", refresh);
    return () => i18n.off("languageChanged", refresh);
  }, [queryClient]);
  return (
    <QueryClientProvider client={queryClient}>
      <ThemeProvider>
        {children}
        <Toaster position="bottom-right" closeButton />
      </ThemeProvider>
    </QueryClientProvider>
  );
}
