# Architecture

Ella MOCN is a Multi-Operator Core Network (MOCN) gateway. It lets one neutral host radio network serve the subscribers of several mobile operators, with each operator keeping its own core network.

[![Neutral host architecture](<https://docs.ellanetworks.com/mocn/images/architecture.svg>)](<https://docs.ellanetworks.com/mocn/images/architecture.svg>)

Neutral host architecture

## What each side sees

- **Radios** connect to Ella MOCN as if it were their core. They broadcast the networks of every operator they serve, and need no per-operator configuration.
- **Each operator** sees Ella MOCN as a single base station. The operator provisions one node, however large the neutral host's network grows.

Adding an operator does not touch the radios, and adding a radio does not involve the operators.

When a phone connects, it picks one of the networks the radio broadcasts, and Ella MOCN sends its signalling to the operator that owns that network.

## Mobility

When a phone moves between radios, the operator's core stays in charge of the handover, as it would with its own radios. Ella MOCN delivers the handover to the right radio, so phones keep their connection as they move through the neutral host's network. The core finds Ella MOCN by tracking area, so the neutral host's tracking areas must not be reused elsewhere in the operator's network.

## Public warnings

The sharing operators agree on one of them to send public warnings, such as emergency alerts, for the whole shared network. Ella MOCN broadcasts that operator's warnings on every radio in the warning area, and does not broadcast warnings from the others. When a radio restarts, Ella MOCN tells that operator, so its warnings are sent to the radio again.
