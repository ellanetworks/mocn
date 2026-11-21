# 3GPP Compliance

Ella MOCN implements 3GPP-standard interfaces for signalling.

> [!NOTE]
>
> Open an [issue](<https://github.com/ellanetworks/mocn/issues>) if you notice a 3GPP-compliance gap or require a new capability for a production deployment.

## Supported

### S1AP (TS 36.413)

- **Management.** S1 Setup, configuration updates, Reset, Error Indication, overload.
- **UE-associated signalling.** Relayed between the radio and the MME, handover included.
- **Paging.**
- **Warning message transmission.**

### NGAP (TS 38.413)

- **Interface management.** NG Setup, configuration updates, NG Reset, Error Indication, AMF Status Indication, overload.
- **UE-associated signalling.** Relayed between the radio and the AMF, handover included.
- **Paging.**
- **Warning message transmission.**

### GTP-U (TS 29.281)

- **User plane.** Relayed between the radios and the operators' gateways, with user-plane aggregation.
