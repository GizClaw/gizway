export type FakeScenario = "active-payg" | "new-user" | "low-credit";

export const fakeScenarios: Array<{ id: FakeScenario; label: string }> = [
  { id: "active-payg", label: "Active PAYG user" },
  { id: "new-user", label: "New user" },
  { id: "low-credit", label: "Low credit" },
];
