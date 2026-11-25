import { useState } from "react";
import { Button, TextField, PasswordField, Card } from "@b4/react";
import { useMutation, useQueryClient } from "@tanstack/react-query";

import { api, ApiError } from "../api/client";

export function Login() {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const queryClient = useQueryClient();

  const loginMutation = useMutation({
    mutationFn: () => api.login(email, password),
    onSuccess: (user) => {
      queryClient.setQueryData(["me"], user);
    },
  });

  return (
    <div style={{ display: "grid", minHeight: "100vh", placeItems: "center" }}>
      <Card style={{ width: 360, padding: 32 }}>
        <h1 style={{ marginTop: 0, fontSize: 20 }}>Sign in</h1>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            loginMutation.mutate();
          }}
          style={{ display: "flex", flexDirection: "column", gap: 16 }}
        >
          <TextField
            label="Email"
            type="email"
            autoComplete="username"
            value={email}
            onValueChange={setEmail}
            required
          />
          <PasswordField
            label="Password"
            autoComplete="current-password"
            value={password}
            onValueChange={setPassword}
            required
          />
          {loginMutation.isError && (
            <p role="alert" style={{ color: "var(--b4-color-danger-text, crimson)", fontSize: 13 }}>
              {loginMutation.error instanceof ApiError
                ? loginMutation.error.message
                : "Something went wrong."}
            </p>
          )}
          <Button type="submit" loading={loginMutation.isPending}>
            Sign in
          </Button>
        </form>
      </Card>
    </div>
  );
}
