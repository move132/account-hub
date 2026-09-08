import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter } from "react-router-dom";
import { App } from "./app/app";
import { AuthProvider } from "./app/auth-context";
import { ThemeProvider } from "./app/theme-provider";
import { ToastProvider } from "./components/ui/toast";
import "./styles/theme.css";
createRoot(document.getElementById("root")!).render(<StrictMode>
  <BrowserRouter>
    <ThemeProvider>
      <ToastProvider>
        <AuthProvider>
          <App />
        </AuthProvider>
      </ToastProvider>
    </ThemeProvider>
  </BrowserRouter>
</StrictMode>);
