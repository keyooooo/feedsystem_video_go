import { resumeCookieSession } from "../api/client";
import { secureSessions } from "../session/auth";
import { StrictMode, Component, type ReactNode } from "react";
import { createRoot } from "react-dom/client";
import { createBrowserRouter, RouterProvider } from "react-router-dom";
import App from "./App";
import "./tokens.css";
class ErrorBoundary extends Component<
  { children: ReactNode },
  { failed: boolean }
> {
  state = { failed: false };
  static getDerivedStateFromError() {
    return { failed: true };
  }
  render() {
    return this.state.failed ? (
      <main style={{ padding: 48 }}>
        <h1>页面暂时遇到问题</h1>
        <p>请刷新页面重试。</p>
        <button onClick={() => location.reload()}>刷新页面</button>
      </main>
    ) : (
      this.props.children
    );
  }
}
const router = createBrowserRouter([
  {
    path: "*",
    element: <App />,
    errorElement: (
      <main style={{ padding: 48 }}>
        <h1>页面暂时遇到问题</h1>
        <button onClick={() => location.reload()}>刷新页面</button>
      </main>
    ),
  },
]);
const root = createRoot(document.getElementById("app")!);
const mount = () =>
  root.render(
    <StrictMode>
      <ErrorBoundary>
        <RouterProvider router={router} />
      </ErrorBoundary>
    </StrictMode>,
  );

if (secureSessions) {
  root.render(<main role="status">正在连接…</main>);
  void resumeCookieSession().finally(mount);
} else {
  mount();
}
