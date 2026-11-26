# User-Plane Aggregation with eBPF

While signalling always goes through Ella MOCN, you can choose to have subscriber traffic go through Ella MOCN too. This is called user-plane aggregation.

## Direct vs aggregated

[![User-plane aggregation](<https://docs.ellanetworks.com/mocn/images/user_plane_aggregation.svg>)](<https://docs.ellanetworks.com/mocn/images/user_plane_aggregation.svg>)

User-plane aggregation

### Direct

Each radio sends its traffic straight to the operator's network. The radios and the operator's network must reach each other, so every radio needs an address the operator can route to.

### Aggregated

Ella MOCN carries the traffic between the radios and the operator's network. The operator sees only Ella MOCN's address, and the radios stay on the neutral host's private network. Subscriber traffic follows the same path as Ella MOCN's signalling to that operator, typically its IPsec tunnel. All of that operator's traffic then depends on Ella MOCN's capacity and availability.

## How Ella MOCN forwards traffic

Ella MOCN uses eBPF to relay subscriber traffic between the radios and the operators' networks in the kernel. With Traffic Control, it puts its own tunnel endpoints in place of the radios' and the operators', so each side only ever sees Ella MOCN. It never touches the traffic inside the tunnels.

## More information

- [How to Enable User-Plane Aggregation](<https://docs.ellanetworks.com/mocn/how_to/enable_user_plane_aggregation/index.md>).
- [How to Connect an Operator over IPsec](<https://docs.ellanetworks.com/mocn/how_to/connect_an_operator_over_ipsec/index.md>).
