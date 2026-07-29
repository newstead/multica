import { describe, expect, it } from "vitest";
import { paths } from "@multica/core/paths";
import { matchRoutes } from "react-router-dom";

import { appRoutes } from "./routes";

describe("desktop app routes", () => {
  it("registers the shared mem0 usage path", () => {
    const matches = matchRoutes(appRoutes, paths.workspace("acme").usageMem0());

    expect(matches?.at(-1)?.route.path).toBe("usage/mem0");
  });

  it("registers the shared Hindsight usage path", () => {
    const matches = matchRoutes(
      appRoutes,
      paths.workspace("acme").usageHindsight(),
    );

    expect(matches?.at(-1)?.route.path).toBe("usage/hindsight");
  });
});
