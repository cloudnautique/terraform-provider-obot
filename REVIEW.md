# Review notes

- **Start a catalog source edit's timeout only after it holds the client's catalog lock.** Every `obot_mcp_catalog_source` in an apply shares one client, and each edit waits for a full catalog sync. A deadline that also counts time queued behind other sources makes later sources time out, before their write, with an error unrelated to them. The lock wait itself must end when the context does. `ModifyCatalogSources` owns both; resources must not wrap it in their own timeout.
