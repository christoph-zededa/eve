# Januscape (CVE-2026-53359) test input

`januscape_cve_2026_53359_poc.c` is the **public** proof-of-concept kernel module
for CVE-2026-53359 ("Januscape"), a guest-to-host use-after-free in the KVM/x86
shadow MMU (`kvm_mmu_get_child_sp()` role-mismatch shadow-page reuse ->
`pte_list_remove()` host DoS).

* Source: oss-security post by Hyunwoo Kim (@v4bel), 2026-07-06
  <https://www.openwall.com/lists/oss-security/2026/07/06/7>
* Copyright (c) 2026 Hyunwoo Kim (@v4bel); licensed GPL (see the module header).
* Reproduced here **verbatim**, as unmodified test input only.

It is used by `TestJanuscapeGuestToHostEscape` (see `../januscape_test.go`), which
boots an Ubuntu VM **edge app** on EVE, compiles this module in-guest against the
Ubuntu kernel (headers installed via cloud-init `apt-get`), and `insmod`s it. The
module enters VMX/SVM operation itself and drives a nested guest, racing EVE's
(the KVM *host*'s) shadow page-table handling. A successful trigger crashes/hangs
**EVE**, not the edge-app VM.

This file is test data: it is copied to the ephemeral guest and compiled there;
it is never compiled into evetest and never runs on the test host.
