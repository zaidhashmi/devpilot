export type Actor = {
  user: { id: string; email: string; display_name: string; status: string };
  organization: { id: string; name: string; slug: string; status: string };
  membership: { id: string; organization_id: string; user_id: string; role: "owner" | "admin" | "member"; status: string };
};
