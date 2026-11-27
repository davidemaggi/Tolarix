import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { vi } from "vitest";

import { apiError, mockApi, webSession } from "@/test/api";
import { renderApp } from "@/test/render";

afterEach(() => vi.unstubAllGlobals());

describe("session gate", () => {
  it("asks for the password and shows the app after signing in", async () => {
    const user = userEvent.setup();
    const { calls } = mockApi({
      "GET /auth/session": () => webSession({ authenticated: false, csrfToken: undefined }),
      "POST /auth/login": ({ body }) =>
        (body as { password: string }).password === "right horse battery"
          ? webSession()
          : apiError(401, "invalid_password"),
    });
    renderApp();

    await user.click(await screen.findByRole("button", { name: "Sign in" }));
    expect(screen.getByText("Enter the password.")).toBeInTheDocument();

    await user.type(screen.getByLabelText("Password"), "wrong");
    await user.click(screen.getByRole("button", { name: "Sign in" }));
    expect(await screen.findByText("Wrong password.")).toBeInTheDocument();

    await user.type(screen.getByLabelText("Password"), "right horse battery");
    await user.click(screen.getByRole("button", { name: "Sign in" }));
    expect(await screen.findByRole("heading", { level: 1, name: "Dashboard" })).toBeInTheDocument();
    expect(calls.filter((c) => c.path === "/auth/login")).toHaveLength(2);
  });

  it("requires a new password after a one-time password", async () => {
    const user = userEvent.setup();
    const { fetch } = mockApi({
      "GET /auth/session": () => webSession({ mustChangePassword: true }),
      "POST /auth/password": () => webSession(),
    });
    renderApp();

    expect(
      await screen.findByRole("heading", { name: "Choose your password" }),
    ).toBeInTheDocument();
    await user.type(screen.getByLabelText("Current password"), "one-time-pw");
    await user.type(screen.getByLabelText("New password"), "short");
    await user.type(screen.getByLabelText("Repeat the new password"), "different");
    await user.click(screen.getByRole("button", { name: "Save and continue" }));
    expect(screen.getByText("Use at least 12 characters.")).toBeInTheDocument();
    expect(screen.getByText("The two passwords do not match.")).toBeInTheDocument();

    await user.clear(screen.getByLabelText("New password"));
    await user.type(screen.getByLabelText("New password"), "a long new passphrase");
    await user.clear(screen.getByLabelText("Repeat the new password"));
    await user.type(screen.getByLabelText("Repeat the new password"), "a long new passphrase");
    await user.click(screen.getByRole("button", { name: "Save and continue" }));

    expect(await screen.findByRole("heading", { level: 1, name: "Dashboard" })).toBeInTheDocument();
    // The CSRF token from the session goes with every change.
    const post = fetch.mock.calls
      .map(([req]) => req as Request)
      .find((r) => r instanceof Request && r.url.endsWith("/auth/password"));
    expect(post?.headers.get("X-CSRF-Token")).toBe("csrf-1");
  });

  it("returns to the sign-in screen when the session expires", async () => {
    let signedIn = true;
    mockApi({
      "GET /auth/session": () => webSession({ authenticated: signedIn }),
      "GET /flows": () => {
        signedIn = false;
        return apiError(401, "unauthorized");
      },
    });
    renderApp("/flows");
    expect(await screen.findByRole("heading", { name: "Sign in" })).toBeInTheDocument();
  });
});
