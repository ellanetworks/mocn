import type { ReactElement } from "react";
import { Navigate, Route, Routes } from "react-router-dom";
import Layout from "@/components/Layout";
import { AuthProvider, useAuth, type Permission } from "@/contexts/AuthContext";
import Dashboard from "@/pages/Dashboard";
import Initialize from "@/pages/Initialize";
import Login from "@/pages/Login";
import OperatorDetail from "@/pages/OperatorDetail";
import Operators from "@/pages/Operators";
import Profile from "@/pages/Profile";
import RadioDetail from "@/pages/RadioDetail";
import Radios from "@/pages/Radios";
import UserDetail from "@/pages/UserDetail";
import Users from "@/pages/Users";

export default function App() {
  return (
    <Routes>
      <Route path="/login" element={<Login />} />
      <Route path="/initialize" element={<Initialize />} />
      <Route
        path="*"
        element={
          <AuthProvider>
            <Layout>
              <Pages />
            </Layout>
          </AuthProvider>
        }
      />
    </Routes>
  );
}

const pages: { path: string; element: ReactElement; permission: Permission }[] =
  [
    { path: "/dashboard", element: <Dashboard />, permission: "user:self" },
    {
      path: "/operators",
      element: <Operators />,
      permission: "operator:read",
    },
    {
      path: "/operators/:name",
      element: <OperatorDetail />,
      permission: "operator:read",
    },
    {
      path: "/radios",
      element: <Radios />,
      permission: "radio:read",
    },
    {
      path: "/radios/:ref",
      element: <RadioDetail />,
      permission: "radio:read",
    },
    { path: "/users", element: <Users />, permission: "user:admin" },
    {
      path: "/users/:email",
      element: <UserDetail />,
      permission: "user:admin",
    },
  ];

function Pages() {
  const auth = useAuth();

  return (
    <Routes>
      {pages
        .filter(({ permission }) => auth.can(permission))
        .map(({ path, element }) => (
          <Route key={path} path={path} element={element} />
        ))}
      <Route path="/profile" element={<Profile />} />
      <Route path="*" element={<Navigate to="/dashboard" replace />} />
    </Routes>
  );
}
