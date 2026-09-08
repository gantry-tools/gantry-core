# agent

`agent` contains provider-neutral run state and terminal outcome classification. It orders user cancellation, request cancellation, output limits, service shutdown, stream errors, provider failures, completion evidence and process exit facts without parsing platform-specific error strings.

The public surface is `RunState`, `Snapshot`, `ExitStatus`, `Classify`, `ClassifyProviderError`, `SanitizeBillingURL`, `ReconcileRecovered` and `RunArgs`. Process creation, provider configuration, HTTP streaming and persistence stay in the application.
