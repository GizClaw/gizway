import { releaseMetadata } from "@/build/release-metadata";

export function GET(): Response {
  return Response.json({
    status: "healthy",
    service: "gizway-web",
    ...releaseMetadata,
    server_time: new Date().toISOString(),
  });
}
