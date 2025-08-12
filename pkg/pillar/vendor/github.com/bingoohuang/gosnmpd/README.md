# gosnmpd

gosnmpd is an SNMP server library fully written in Go. It provides Server Get, GetNext, GetBulk, Walk, BulkWalk, Set and
Traps. It supports IPv4 and IPv6, using __SNMPv2c__ or __SNMPv3__. Builds are tested against linux/amd64 and linux/386.

## TL;DR

Build your own SNMP Server, try this:

```shell
$ go install github.com/bingoohuang/gosnmpd/cmd/gosnmpd@latest
$ gosnmpd
$ snmpwalk -v 3 -l authNoPriv -n public -u testuser -a md5 -A testauth 127.0.0.1:1161 1
```

```shell
$ gosnmpd -x des -X testpriv
$ snmpwalk -v 3 -l authPriv  -n public -u testuser -a md5 -A testauth -x des -X testpriv 127.0.0.1:1161 1
```

## Quick Start

```golang

master := gosnmpd.MasterAgent{
    Logger: gosnmpd.NewDefaultLogger(),
    SecurityConfig: gosnmpd.SecurityConfig{
        AuthoritativeEngineBoots: 1,
        Users: []gosnmp.UsmSecurityParameters{
            {
                UserName:                 c.String("v3Username"),
                AuthenticationProtocol:   gosnmp.MD5,
                PrivacyProtocol:          gosnmp.DES,
                AuthenticationPassphrase: c.String("v3AuthenticationPassphrase"),
                PrivacyPassphrase:        c.String("v3PrivacyPassphrase"),
            },
        },
    },
    SubAgents: []*gosnmpd.SubAgent{
        {
            CommunityIDs: []string{c.String("community")},
            OIDs:         mibImps.All(),
        },
    },
}
server := gosnmpd.NewSNMPServer(master)
err := server.ListenUDP("udp", "127.0.0.1:1161")
if err != nil {
    logger.Errorf("Error in listen: %+v", err)
}
server.ServeForever()
```

## Serve your own oids

This library provides some common oid for use. See [mibImps](https://github.com/bingoohuang/gosnmpd/tree/master/mibImps)
for code

Append `gosnmpd.PDUValueControlItem` to your SubAgent OIDS:

```golang
{
    OID:      fmt.Sprintf("1.3.6.1.2.1.2.2.1.1.%d", ifIndex),
    Type:     gosnmp.Integer,
    OnGet:    func() (value interface{}, err error) { return gosnmpd.Asn1IntegerWrap(ifIndex), nil },
    Document: "ifIndex",
},
```

Supports Types:  See RFC-2578 FOR SMI
- Integer
- OctetString
- ObjectIdentifier
- IPAddress
- Counter32
- Gauge32
- TimeTicks
- Counter64
- Uinteger32
- OpaqueFloat
- OpaqueDouble

Could use wrap function for detect type error. See `gosnmpd.Asn1IntegerWrap` / `gosnmpd.Asn1IntegerUnwrap` and so on.

## Thanks

This library is based on **[soniah/gosnmp](https://github.com/soniah/gosnmp)** for encoder / decoders. (made a [fork](https://github.com/slayercat/gosnmp) for maintenance)

## logs

```log
$ gosnmpd
$ snmpwalk -v 3 -l authNoPriv  -n public -u testuser -a md5 -A testauth 127.0.0.1:1161 1
$ snmp -t 127.0.0.1:1161 -userName testuser -authPassword testauth -o 1.3.6.1.4.1.2021.y -y=4.5 -y=4.6 -o 1.3.6.1.4.1.2021.9.1.z.1 -z=7-8
[get][0][UCD-SNMP-MIB::memTotalReal][.1.3.6.1.4.1.2021.4.5] => Integer: 16777216
[get][1][UCD-SNMP-MIB::memAvailReal][.1.3.6.1.4.1.2021.4.6] => Integer: 6013352
[get][2][UCD-SNMP-MIB::dskAvail.1][.1.3.6.1.4.1.2021.9.1.7.1] => Integer: 17481
[get][3][UCD-SNMP-MIB::dskUsed.1][.1.3.6.1.4.1.2021.9.1.8.1] => Integer: 221590
```

```log
$ gosnmpd -x des -X testpriv
$ snmpwalk -v 3 -l authPriv -n public -u testuser -a md5 -A testauth -x des -X testpriv 127.0.0.1:1161 1
DISMAN-EVENT-MIB::sysUpTimeInstance = Timeticks: (4424282) 12:17:22.82
IF-MIB::ifIndex.0 = INTEGER: 1
IF-MIB::ifIndex.1 = INTEGER: 1
IF-MIB::ifDescr.0 = STRING: eth0
IF-MIB::ifDescr.1 = STRING: lo
IF-MIB::ifType.0 = INTEGER: gigabitEthernet(117)
IF-MIB::ifType.1 = INTEGER: gigabitEthernet(117)
IF-MIB::ifPhysAddress.0 = STRING: 52:54:0:c6:32:4a
IF-MIB::ifPhysAddress.1 = STRING:
IF-MIB::ifAdminStatus.0 = INTEGER: up(1)
IF-MIB::ifAdminStatus.1 = INTEGER: up(1)
IF-MIB::ifOperStatus.0 = INTEGER: 0
IF-MIB::ifOperStatus.1 = INTEGER: 0
IF-MIB::ifInOctets.0 = Counter32: 4131263897
IF-MIB::ifInOctets.1 = Counter32: 253267255
IF-MIB::ifInUcastPkts.0 = Counter32: 21906941
IF-MIB::ifInUcastPkts.1 = Counter32: 223426
IF-MIB::ifInDiscards.0 = Counter32: 0
IF-MIB::ifInDiscards.1 = Counter32: 0
IF-MIB::ifInErrors.0 = Counter32: 0
IF-MIB::ifInErrors.1 = Counter32: 0
IF-MIB::ifOutOctets.0 = Counter32: 4039169924
IF-MIB::ifOutOctets.1 = Counter32: 253270719
IF-MIB::ifOutUcastPkts.0 = Counter32: 20826025
IF-MIB::ifOutUcastPkts.1 = Counter32: 223442
IF-MIB::ifOutDiscards.0 = Counter32: 0
IF-MIB::ifOutDiscards.1 = Counter32: 0
IF-MIB::ifOutErrors.0 = Counter32: 0
IF-MIB::ifOutErrors.1 = Counter32: 0
UCD-SNMP-MIB::memIndex = INTEGER: 1
UCD-SNMP-MIB::memErrorName = STRING: swap
UCD-SNMP-MIB::memTotalSwap = INTEGER: 0 kB
UCD-SNMP-MIB::memAvailSwap = INTEGER: 0 kB
UCD-SNMP-MIB::memTotalReal = INTEGER: 3825904 kB
UCD-SNMP-MIB::memAvailReal = INTEGER: 3127956 kB
UCD-SNMP-MIB::memTotalFree = INTEGER: 3127956 kB
UCD-SNMP-MIB::memMinimumSwap = INTEGER: 0 kB
UCD-SNMP-MIB::memBuffer = INTEGER: 120368 kB
UCD-SNMP-MIB::memCached = INTEGER: 2905776 kB
UCD-SNMP-MIB::memSwapError = INTEGER: noError(0)
UCD-SNMP-MIB::memSwapErrorMsg = STRING:
UCD-SNMP-MIB::dskIndex.1 = INTEGER: 1
UCD-SNMP-MIB::dskPath.1 = STRING: /
UCD-SNMP-MIB::dskDevice.1 = STRING: /
UCD-SNMP-MIB::dskTotal.1 = INTEGER: 80569
UCD-SNMP-MIB::dskAvail.1 = INTEGER: 39830
UCD-SNMP-MIB::dskUsed.1 = INTEGER: 37364
UCD-SNMP-MIB::dskPercent.1 = INTEGER: 48
UCD-SNMP-MIB::laIndex.1 = INTEGER: 1
UCD-SNMP-MIB::laIndex.2 = INTEGER: 2
UCD-SNMP-MIB::laIndex.3 = INTEGER: 3
UCD-SNMP-MIB::laNames.1 = STRING: Load-1
UCD-SNMP-MIB::laNames.2 = STRING: Load-5
UCD-SNMP-MIB::laNames.3 = STRING: Load-15
UCD-SNMP-MIB::laLoad.1 = STRING: 0.06
UCD-SNMP-MIB::laLoad.2 = STRING: 0.06
UCD-SNMP-MIB::laLoad.3 = STRING: 0.06
UCD-SNMP-MIB::laLoadInt.1 = INTEGER: 0
UCD-SNMP-MIB::laLoadInt.2 = INTEGER: 0
UCD-SNMP-MIB::laLoadInt.3 = INTEGER: 0
UCD-SNMP-MIB::ssIndex = INTEGER: 0
UCD-SNMP-MIB::ssErrorName = STRING: systemStats
UCD-SNMP-MIB::ssCpuRawUser = Counter32: 22914
UCD-SNMP-MIB::ssCpuRawNice = Counter32: 617
UCD-SNMP-MIB::ssCpuRawSystem = Counter32: 20236
UCD-SNMP-MIB::ssCpuRawIdle = Counter32: 17639225
UCD-SNMP-MIB::ssCpuRawWait = Counter32: 3168
UCD-SNMP-MIB::ssCpuRawInterrupt = Counter32: 0
UCD-SNMP-MIB::ssIORawSent = Counter32: 26507807
UCD-SNMP-MIB::ssIORawReceived = Counter32: 575525
UCD-SNMP-MIB::ssRawInterrupts = Counter32: 1080137212
UCD-SNMP-MIB::ssRawContexts = Counter32: 1546886557
UCD-SNMP-MIB::ssCpuRawSoftIRQ = Counter32: 537
UCD-SNMP-MIB::ssCpuRawSteal = Counter32: 0
UCD-SNMP-MIB::ssCpuRawGuest = Counter32: 0
UCD-SNMP-MIB::ssCpuRawGuest = No more variables left in this MIB View (It is past the end of the MIB tree)
```
