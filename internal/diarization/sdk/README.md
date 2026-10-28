# NeMo-Speech.cpp C ABI

Unmodified Apache-2.0 public headers from NVIDIA/NeMo-Speech.cpp commit
`66d50d91b75a406becd95fbaf39d2c9c13e95d7c`. The container builds the same revision.

The cgo bridge loads the installed SDK library at model initialization, validates
its required symbols, and fails startup if it is unavailable. There is no CLI or
Python fallback. Dynamic loading lets model-independent tests run without loading
NeMo, and keeps NeMo's ggml symbols local to its runtime.

`MODEL-LICENSE.OpenMDW-1.1` is the unmodified model license from
OpenMDW/OpenMDW commit `b26b32b34ad2edcc29a7707abb68dcfb25a538c1`,
path `1.1/LICENSE.OpenMDW-1.1`. The SDK's Apache license and the model's
OpenMDW license are separate; both ship in the container.
