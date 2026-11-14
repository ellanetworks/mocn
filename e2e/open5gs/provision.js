const gbps = { value: NumberInt(1), unit: NumberInt(3) };

const provision = (name, imsi, msisdn) =>
  db.getSiblingDB(name).subscribers.replaceOne(
    { imsi: imsi },
    {
      schema_version: NumberInt(1),
      imsi: imsi,
      msisdn: [msisdn],
      imeisv: [],
      mme_host: [],
      mm_realm: [],
      purge_flag: [],
      slice: [
        {
          sst: NumberInt(1),
          default_indicator: true,
          session: [
            {
              name: "internet",
              type: NumberInt(3),
              qos: {
                index: NumberInt(9),
                arp: {
                  priority_level: NumberInt(8),
                  pre_emption_capability: NumberInt(1),
                  pre_emption_vulnerability: NumberInt(1),
                },
              },
              ambr: { downlink: gbps, uplink: gbps },
              pcc_rule: [],
              _id: new ObjectId(),
            },
          ],
          _id: new ObjectId(),
        },
      ],
      security: {
        k: "465B5CE8B199B49FAA5F0A2EE238A6BC",
        op: null,
        opc: "CD63CB71954A9F4E48A5994E37A02BAF",
        amf: "8000",
        sqn: NumberLong(64),
      },
      ambr: { downlink: gbps, uplink: gbps },
      access_restriction_data: NumberInt(32),
      network_access_mode: NumberInt(0),
      subscriber_status: NumberInt(0),
      operator_determined_barring: NumberInt(0),
      subscribed_rau_tau_timer: NumberInt(12),
      __v: 0,
    },
    { upsert: true },
  );

provision("open5gs", "001010000000001", "0000000001");
provision("open5gs_b", "001020000000002", "0000000002");
provision("open5gs", "001010000000011", "0000000011");
provision("open5gs_b", "001020000000012", "0000000012");
provision("open5gs", "001010000000021", "0000000021");
