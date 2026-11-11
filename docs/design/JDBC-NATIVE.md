# JDBC-NATIVE: foundation of the JDBC driver

Design for catalog part JDBC-DESIGN-NATIVE. It specifies everything below the JDBC
classes of the stuntgres JDBC driver in `bindings/jdbc`: the build, the C JNI shim, the
Java package `org.stuntgres.pq`, the native loader and the JUnit 5 test harness with its
four modes. [docs/LIBSPQ.md](../LIBSPQ.md) is the contract of the C library the driver
runs on; [LIBSPQ-CABI.md](LIBSPQ-CABI.md) fixes its names, values and prototypes. Where
this document says "CABI n.n" it means that section of LIBSPQ-CABI.md.

Statements marked **(measured)** were observed in scratch code on 2026-11-11 on the tree
at commit `0c704a82ce` with JDK 17.0.13 and 21.0.6 (Temurin), gcc 13.3, go 1.27.1,
Maven 3.9.9, PostgreSQL 18.6 from the pinned image and pgjdbc 42.7.13 (section 12).
Every code block that shows a complete file is the file that ran in those experiments,
unless the block's introduction says "not run".

The owner's decisions J1 to J12 and the architecture points B1 to B15 of the approved
plan are fixed and are not repeated here. The code, the scripts and the tests of this
document were written for it; pgjdbc and OpenJDK were read and run only to learn
observable behavior.

## 1. Purpose, layering, layout

```
org.stuntgres.Driver, org.stuntgres.jdbc.*, org.stuntgres.core.*      later design parts
        │ PqConnection, PqResult, PqCancel, PqNotice, PqNotification, PqException
org.stuntgres.pq                     the only package with native methods (section 9)
        │ JNI: 35 native methods (section 5)
libstuntgres_jni.so                  the shim, C11, src/main/c (sections 4 to 7)
        │ dlopen + dlsym with a symbol prefix; no link-time dependency
libspq.so ("SPQ")      |      libpq.so.5 of PostgreSQL 18 ("PQ", tests only)
```

### 1.1 Fixed points and what the experiments did to them

| # | Fixed point | Result |
|---|---|---|
| F1 | **Coexistence.** A Go c-shared library and HotSpot work in one process. | Confirmed on both JDKs with no remedy (section 2). |
| F2 | **Layout.** `bindings/jdbc` holds `README.md`, `pom.xml`, `Makefile`, `.gitignore`, `src/main/java`, `src/main/c`, `src/main/resources`, `src/test/java`, `src/test/resources`, `tools`; no Go file; all output under `bindings/jdbc/target`. | Kept. Added: `src/main/resources-filtered` (the one filtered resource) and `src/test/c` (stand-in libraries of the loader tests). Directories without a file yet (`src/main/resources`, `src/test/resources`) are created by the first part that puts a file there; nothing depends on their existence **(measured: Maven skips a missing resource directory)**. |
| F3 | **pom.xml** written once, completely. | Kept; one finding changed its content: the JDK that compiles and runs the tests is named by the property `stuntgres.jdk.home`, because the `mvn` launcher of a host may pin its own JDK (3.3). No later part edits `pom.xml`. |
| F4 | **Makefile** written once, completely, targets `shim`, `compile`, `check`, `clean`. | Kept; added target `jar` (the jar with the native files, used later by `make jdbc`) and the internal targets `jdk` and `test-natives`. No later part edits it. |
| F5 | **Shim loading** by `dlopen` and `dlsym` with a prefix, signals blocked, once per process. | Confirmed (section 4). Flags `RTLD_NOW` and `RTLD_LOCAL`. Two additions: dependencies can be opened first (libpq 18 from the image needs two libraries the host lacks), and a process-wide check refuses a second copy of the target library. Only one prefix is passed: the shim binds no `spg_` function. |
| F6 | **JNI surface**: blocking functions only, no critical regions, no upcalls except constructors. | Kept. **Redesigned:** connect uses `connectStart`, installs the notice receiver, then drives `connectPoll` itself (7.1, experiment 10): with the blocking connect function a notice raised while the session starts is printed to `stderr`. **Extended:** parameters carry a format (text or binary) and a length, because pgjdbc's observable answer to a NUL character in a string parameter can only be reproduced with a binary-format value (5.6, experiment 7). |
| F7 | **Result copy**: one native call, one Java object, `clear` on every path. | Confirmed: 0.6 ms for 100,000 values against 3.9 to 5.1 ms with a JNI call per value (section 6). |
| F8 | **Notices** queued in C, drained by Java. | Kept; the receiver is installed before the session starts (see F6). |
| F9 | **Handles**: a `long` in one wrapper, idempotent close, a `Cleaner` as fallback. | Kept; every method that passes a handle to native code ends with `Reference.reachabilityFence(this)` (9.1). |
| F10 | **One exception type** for failures of the boundary. | Kept: `PqException` with a kind. **Extended** by three kinds: `CALL_FAILED` (a library function reported failure without a result), `CONNECT_FAILED` and `UNSUPPORTED` (9.3). |
| F11 | **Loader**: system property, else resources extracted to a private directory. | Kept (section 8). The directory is named after the checksums of the files; a second class loader gets a private copy of the shim; `libspq.so` is mapped once. |
| F12 | **Harness**: four modes through `make -C bindings/jdbc check MODE=...`. | Kept (section 10). A script starts and removes the PostgreSQL container; the test JVM starts the stuntgres server processes, and the script removes whatever is left. Isolation: a database per test class in every mode but native. |
| F13 | **JDK 17 and 21.** | The scratch suite passes on both; a run uses the JDK of `JAVA_HOME`. |
| F14 | **Parallelism**: one C file, one Java file, one test file per family and part. | Kept: the connection class is a chain of abstract classes, one file per part, with stub files (11.2). |

### 1.2 Files of this foundation

Paths are relative to `bindings/jdbc`. "Owner" is the part that creates the file; a file
marked *stub* is created by one part and replaced as a whole by its owner (11.2).

| File | Responsibility | Owner |
|---|---|---|
| `README.md` | Contract of the directory. | BUILD-01 |
| `.gitignore` | The single line `/target/`. | BUILD-01 |
| `pom.xml` | Maven build (3.1). | BUILD-01 |
| `Makefile` | Entry point of every build and test run (3.2). | BUILD-01 |
| `tools/check_jdk.sh` | Fails unless `JAVA_HOME` is a JDK 17 or newer. | BUILD-01 |
| `tools/check_jni.sh` | Native declarations and exported C functions agree. | BUILD-01 |
| `tools/stage_natives.sh` | Stages the native files of `make jar`. | BUILD-01 |
| `src/main/java/org/stuntgres/pq/package-info.java` | Package documentation. | BUILD-01 |
| `src/main/resources-filtered/org/stuntgres/version.properties` | `version=${project.version}`. | BUILD-01 |
| `tools/run_mode.sh` | Runs Maven for one mode; starts and stops the mode's backend. | BUILD-02 |
| `src/test/java/org/stuntgres/testing/UnitTest.java`, `Mode.java`, `Waits.java`, `ChildJvm.java` | Tag of unit tests, the mode, bounded waits, child JVMs. | BUILD-02 |
| `src/test/java/org/stuntgres/testing/SkeletonTest.java`, `WaitsTest.java`, `ChildJvmTest.java` | Tests of the above. | BUILD-02 |
| `src/main/java/org/stuntgres/pq/ConnStatus.java`, `ExecStatus.java`, `TransactionStatus.java`, `DiagField.java`, `Feature.java` | Constants (9.2). | PQ-01A |
| `src/main/java/org/stuntgres/pq/PqException.java`, `PqText.java`, `Conninfo.java`, `PqNotice.java`, `PqNotification.java` | Exception, text helpers, value classes (9.3). | PQ-01B |
| `src/main/c/sjni.h` | The private header, complete (4.1). | PQ-02A |
| `src/main/c/sjni_check.c` | Compile-time ties between the header and the Java constants. | PQ-02A |
| `src/main/c/sjni_util.c` | Helpers of every C file; defines the function table. | PQ-02B |
| `src/test/c/fakepq.c`, `tools/build_test_natives.sh` | Stand-in target libraries of the unit tests. | PQ-02B |
| `src/main/c/sjni_load.c`, `src/main/java/org/stuntgres/pq/PqLibrary.java` | Loading the target library; feature flags. | PQ-02C |
| `src/main/java/org/stuntgres/pq/NativeLoader.java` | The loader (section 8). | PQ-03A |
| `src/main/java/org/stuntgres/pq/NativeExtractor.java` | Extraction of the native files of the class path. | *stub* PQ-03A, PQ-03B |
| `src/test/java/org/stuntgres/testing/DatabaseTest.java`, `ReferenceToo.java`, `NativeOnly.java`, `DatabaseExtension.java`, `Backend.java`, `Props.java`, `TestDatabase.java` | Support API of database tests (10.6). | TEST-01A |
| `src/test/java/org/stuntgres/testing/Modes.java`, `RequiresFeature.java`, `GateCondition.java` | The two gates (10.7). | TEST-01G |
| `tools/oracle-image.txt`, `tools/oracle_container.sh`, `tools/libpq18.sh`, `tools/mode_reference.sh`, `tools/mode_oracle.sh`, `src/test/java/org/stuntgres/testing/ContainerBackend.java` | Reference and oracle mode (10.3, 10.4). | TEST-01B |
| `tools/mode_server.sh`, `src/test/java/org/stuntgres/testing/ServerBackend.java` | Server mode (10.5). | TEST-01C |
| `src/main/c/sjni_conn.c`, `src/main/java/org/stuntgres/pq/ConnBase.java` | Connect, finish, status, error message. | PQ-04A |
| `src/main/java/org/stuntgres/pq/HandleRelease.java` | Release of a handle; with a `Cleaner` after PQ-13. | *stub* PQ-04A, PQ-13 |
| `src/main/c/sjni_notice.c` | Notice receiver and queue. | *stub* PQ-04A, PQ-08 |
| `src/main/c/sjni_session.c`, `src/main/java/org/stuntgres/pq/ConnSession.java`, `PqConnection.java` | Session accessors; the public connection class. | PQ-04B |
| `src/main/java/org/stuntgres/pq/ConnNotices.java` | `takeNotices`. | *stub* PQ-04B, PQ-08 |
| `src/main/java/org/stuntgres/pq/ConnExec.java`, `src/main/c/sjni_exec.c` | Simple execution. | *stub* PQ-04B (Java), PQ-06A |
| `src/main/java/org/stuntgres/pq/ConnParams.java`, `src/main/c/sjni_params.c` | Parameterized execution. | *stub* PQ-04B (Java), PQ-06B |
| `src/main/java/org/stuntgres/pq/ConnPrepared.java`, `src/main/c/sjni_prepared.c` | Prepared statements. | *stub* PQ-04B (Java), PQ-07 |
| `src/main/java/org/stuntgres/pq/ConnNotify.java`, `src/main/c/sjni_notify.c` | Notifications. | *stub* PQ-04B (Java), PQ-10 |
| `src/main/java/org/stuntgres/pq/ConnCopy.java`, `src/main/c/sjni_copy.c` | COPY. | *stub* PQ-04B (Java), PQ-11 |
| `src/main/java/org/stuntgres/pq/ConnEscape.java`, `src/main/c/sjni_escape.c` | Escaping, client encoding. | *stub* PQ-04B (Java), PQ-12 |
| `src/main/java/org/stuntgres/pq/PqResult.java` | The result object (6.1). | PQ-05A |
| `src/main/c/sjni_result.c`, `src/main/java/org/stuntgres/pq/ResultCopy.java` | The result copy (6.2). | PQ-05B |
| `src/main/c/sjni_cancel.c`, `src/main/java/org/stuntgres/pq/PqCancel.java` | The cancel handle. | PQ-09 |
| `src/main/c/sjni_cluster.c`, `src/main/java/org/stuntgres/pq/PqCluster.java`, `src/test/c/fake_cluster.c` | `SPQXversion`, `SPQXclusterDrop`. | PQ-14A |
| `tools/mode_native.sh`, `src/test/java/org/stuntgres/testing/NativeBackend.java` | Native mode (10.8). | TEST-02 |
| `src/main/c/sjni_ext.c`, `src/main/java/org/stuntgres/pq/PqExt.java` | `SPQXreset`, `SPQXdump`, `SPQXrestore`. | PQ-14B |
| `src/test/java/org/stuntgres/pq/*Test.java`, `*Probe.java`; `src/test/java/org/stuntgres/testing/*Test.java` | One test file per part, named in 11.3. | the part |

The directory contains no Go file, so `go build ./...`, `go vet ./...` and
`go test ./...` never see it **(measured: `go list ./...` in the worktree prints no path
under `bindings`; the directory does not exist before BUILD-01 and gets only the file
types listed above)**. Build output: Maven's and the shim's output is
`bindings/jdbc/target`, ignored by `bindings/jdbc/.gitignore`. These families edit no
root file: the root `Makefile` and the root `.gitignore` belong to JDBC-DESIGN-FEATURES.

Server mode builds `cmd/stuntgres` into `bindings/jdbc/target/stuntgres`, and the
extracted libpq 18 lives in `bindings/jdbc/target/libpq18`; both are inside the ignored
directory.


## 2. Coexistence of the JVM and the engine's runtime

libspq contains a Go runtime. When it is loaded into a running JVM the Go runtime
replaces HotSpot's handlers for SIGSEGV, SIGBUS, SIGFPE and SIGPIPE and adds
`SA_ONSTACK` to the JVM's other handlers; HotSpot depends on those signals for implicit
null checks, safepoint polls, division by zero and `StackOverflowError`. Whether that
works was the gate of this design. It does.

### 2.1 What was measured

Two Go c-shared libraries built with go 1.27.1 were loaded through a scratch JNI shim
that does what section 4.2 prescribes (`pthread_sigmask` to block every signal, `dlopen`,
restore the mask, `dlsym`):

- **slice**: the validation slice of the libspq design (`.cswd/tmp/LIBSPQ/cabi/`), 2.2 MB,
  whose `SPQexec` waits 20 ms in C `poll()`;
- **engine**: a cgo package that embeds the real engine through the root `database/sql`
  driver (94.7 MB, flags `STATIC_TLS` and `NODELETE`) and exports C functions to open a
  session, run a statement in Go, run a statement on a goroutine while the caller waits
  in C `poll()` (the way libspq waits, CABI F4), and block in `poll()` until another
  thread wakes it. Eight goroutines ran statements and allocated inside Go for the whole
  run (220 to 245 Go garbage collections per 60 s run).

Every run lasted 60 seconds. Eight Java threads were inside native calls of the library
all the time. Each stress case ran on two groups of threads: threads that call into the
library between batches, and threads that never do. Every exception was counted and
compared with the number expected, and every native result was compared with a literal.
A calibration with `strace` showed that with `-XX:TieredStopAtLevel=1` each implicit
`NullPointerException` is one SIGSEGV, each integer division by zero one SIGFPE and each
`StackOverflowError` one SIGSEGV; the null-check and division cases therefore ran with
that flag, all others with the default compiler tiers.

Results, identical for both libraries **(measured; 86 runs of the matrix and 6 with libjsig, log.txt experiment 1; the numbers are those of the engine library)**:

| Case | JDK 17.0.13 | JDK 21.0.6 |
|---|---|---|
| implicit `NullPointerException` in compiled code, one signal each | 11.7 million (engine), all caught, none lost | 22.9 million |
| `StackOverflowError`, caught and repeated, on both groups of threads | 29,054 | 42,925 |
| integer division by zero, one signal each | 12.8 million | 24.8 million |
| allocation pressure on 32 threads with two `System.gc()` loops | 17.1 million allocations checked, 6,435 collections | 13.8 million, 6,773 |
| thread dump by SIGQUIT and by `jcmd Thread.print`, every 5 s under full load | 11 of each, native threads shown `RUNNABLE` in the native method | 11 of each |
| JFR recording (`settings=profile`), which samples with the suspend signal | 10,491 execution samples and 2,424 native-method samples, run correct | 10,022 and 2,446 |
| collectors G1, Parallel, Serial, ZGC, generational ZGC (21), Shenandoah | all correct | all correct |
| `-Xcheck:jni` | correct; warnings below | correct; warnings below |
| `-Xrs` | correct | correct |
| return from `main`, `System.exit(7)`, SIGTERM, each with open sessions and a thread blocked in the library | exit 0, 7, 143; the shutdown hook ran | the same |
| all of the above without blocking signals around `dlopen` (central cases) | correct | correct |
| `LD_PRELOAD` of the JDK's `libjsig.so` (not needed; tried to see what it does) | correct; no `-Xcheck:jni` warning | the same |

No run crashed, hung, lost an exception or returned a wrong value. No remedy was needed,
so the list of remedies of the part record (libjsig, a JVM flag, a different way of
loading) was not exercised as a remedy.

**Signal masks of the Go runtime's threads** (`SigBlk` in `/proc/<pid>/task/*/status`)
**(measured)**:

| Loading | `SigBlk` of every Go thread | Meaning |
|---|---|---|
| all signals blocked around `dlopen` | `fffffffc7bba7a27` | Everything is blocked except the synchronous signals (SIGILL, SIGTRAP, SIGBUS, SIGFPE, SIGSEGV), SIGURG, SIGPROF and the signals that cannot be blocked. SIGTERM, SIGINT, SIGHUP, SIGQUIT and SIGUSR2 are never delivered to a Go thread. |
| no blocking | `0000000000000004` | The mask of a Java thread: only SIGQUIT is blocked. A process-directed SIGTERM can land on a Go thread; the JVM still handled it correctly in the experiment. |

The shim blocks signals as docs/LIBSPQ.md section 7 rule 4 requires; the experiment
shows that this keeps the JVM's asynchronous signals away from Go threads, and that the
JVM also survives without it.

**`-Xcheck:jni` output caused by the Go runtime** **(measured)**. The JVM compares
handler addresses and flags periodically and prints, once per signal:

```
Warning: SIGSEGV handler modified!
Signal Handlers:
   SIGSEGV: runtime.cgoSigtramp.abi0 in libgate.so, mask=11111111011111111101111111111110, flags=SA_ONSTACK|SA_RESTART|SA_SIGINFO, unblocked
  *** Handler was modified!
  *** Expected: javaSignalHandler in libjvm.so, mask=11100100110111111111111111111110, flags=SA_RESTART|SA_SIGINFO
...
Consider using jsig library.
```

JDK 17 prints `Warning: <signal> handler modified!` followed by the whole table for
SIGSEGV, SIGILL, SIGFPE, SIGBUS, SIGPIPE, SIGXFSZ and SIGUSR2 (seven times); JDK 21
prints the warning line for SIGSEGV, SIGILL, SIGFPE, SIGBUS and SIGUSR2 and the table
once. SIGSEGV, SIGBUS, SIGFPE and SIGPIPE show Go's handler; SIGILL, SIGXFSZ and SIGUSR2
keep the JVM's handler with `SA_ONSTACK` added. These are warnings; the JVM takes no
action. A real libpq causes none of them, which is why oracle and server mode run under
`-Xcheck:jni` and native mode does not (3.1).

### 2.2 Rules that follow

For the shim (section 4.2):

1. Load the target library once per process, by absolute path, with every signal blocked
   on the loading thread around `dlopen`.
2. Never unload it (`dlclose` is only called for a library that failed the symbol check;
   for a Go library it has no effect).
3. Call only functions that wait in C. A thread inside a blocking call is in native
   state, does not delay a safepoint and shows up in thread dumps as `RUNNABLE` in the
   native method.

For consumers (text for `docs/JDBC.md`, which JDBC-DESIGN-CORE writes):

> **JVM requirements.** The driver runs on HotSpot JDK 17 and 21 with every collector
> (G1, Parallel, Serial, ZGC, Shenandoah) and needs no JVM option. Do not pass
> `-XX:-UseSignalChaining` or `-XX:+AllowUserSignalHandlers`; they were not tested. A
> preloaded `libjsig.so` is not needed and does no harm. Under `-Xcheck:jni` the JVM
> prints `Warning: SIGSEGV handler modified!` (and the same for SIGBUS, SIGFPE, SIGILL,
> SIGUSR2 and, on JDK 17, SIGPIPE and SIGXFSZ) once after the first connection, because
> the engine's runtime installs signal handlers; the warnings are harmless. `-Xrs` works.
> Shutdown hooks run on normal exit, `System.exit` and SIGTERM while connections are
> open. A thread that waits for the database is inside a native call: it cannot be
> interrupted with `Thread.interrupt`; use `Statement.cancel` or a query timeout. A
> virtual thread that calls the driver pins its carrier thread for the duration of the
> call. On JDK 24 and later add `--enable-native-access=ALL-UNNAMED` (class path) to
> avoid the JVM's warning about native access.

Not measured, and stated as such: JDK versions other than 17.0.13 and 21.0.6; JDK 24
and later, where `System.load` is a restricted method (no such JDK is installed; the
option `--enable-native-access=ALL-UNNAMED` is accepted by JDK 17 and 21 **(measured)**
and the test JVM is always started with it, 3.1); `aarch64`; a JVM embedded in another
runtime that installs signal handlers after the library is loaded; virtual threads.


## 3. Build

Everything goes through `bindings/jdbc/Makefile`. It calls Maven three times at most:
`compile` (which also writes the JNI headers with `javac -h`), then the C compiler for
the shim, then `test` or `package`. Maven always runs offline (`-o`).

### 3.1 `pom.xml`

The complete file (BUILD-01 writes it; no later part edits it):

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!-- Build of the stuntgres JDBC driver. Run it through the Makefile next to this file
     (make check MODE=...); the Makefile builds the native shim between the compile and
     the test phase. Every plugin version is pinned; the build works offline (mvn -o). -->
<project xmlns="http://maven.apache.org/POM/4.0.0"
         xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
         xsi:schemaLocation="http://maven.apache.org/POM/4.0.0 https://maven.apache.org/xsd/maven-4.0.0.xsd">
  <modelVersion>4.0.0</modelVersion>

  <groupId>org.stuntgres</groupId>
  <artifactId>stuntgres-jdbc</artifactId>
  <version>0.1.0</version>
  <packaging>jar</packaging>
  <name>stuntgres JDBC driver</name>
  <description>JDBC driver that runs the stuntgres engine in-process through libspq.</description>

  <properties>
    <project.build.sourceEncoding>UTF-8</project.build.sourceEncoding>
    <project.reporting.outputEncoding>UTF-8</project.reporting.outputEncoding>
    <maven.compiler.release>17</maven.compiler.release>
    <junit.version>5.12.2</junit.version>
    <pgjdbc.version>42.7.13</pgjdbc.version>
    <!-- Test mode; set by the Makefile (MODE=...). The profiles below derive the tag
         expression and the JVM arguments of the forked test JVM from it. -->
    <stuntgres.test.mode>unit</stuntgres.test.mode>
    <stuntgres.test.groups>unit</stuntgres.test.groups>
    <stuntgres.test.jvmArgs></stuntgres.test.jvmArgs>
    <!-- The JDK that compiles and runs the tests; the Makefile passes its JAVA_HOME.
         Maven itself may run on another JDK. -->
    <stuntgres.jdk.home>${java.home}</stuntgres.jdk.home>
  </properties>

  <dependencies>
    <dependency>
      <groupId>org.junit.jupiter</groupId>
      <artifactId>junit-jupiter</artifactId>
      <version>${junit.version}</version>
      <scope>test</scope>
    </dependency>
    <dependency>
      <groupId>org.postgresql</groupId>
      <artifactId>postgresql</artifactId>
      <version>${pgjdbc.version}</version>
      <scope>test</scope>
      <exclusions>
        <exclusion>
          <groupId>org.checkerframework</groupId>
          <artifactId>checker-qual</artifactId>
        </exclusion>
      </exclusions>
    </dependency>
  </dependencies>

  <build>
    <resources>
      <!-- Copied as they are: META-INF/services, fixed data files. -->
      <resource>
        <directory>src/main/resources</directory>
      </resource>
      <!-- Copied with ${...} replaced: files that need the project version. -->
      <resource>
        <directory>src/main/resources-filtered</directory>
        <filtering>true</filtering>
      </resource>
      <!-- Native files staged by "make jar": META-INF/native/linux-<arch>/... -->
      <resource>
        <directory>${project.build.directory}/native-resources</directory>
      </resource>
    </resources>
    <plugins>
      <plugin>
        <groupId>org.apache.maven.plugins</groupId>
        <artifactId>maven-resources-plugin</artifactId>
        <version>3.3.1</version>
        <configuration>
          <propertiesEncoding>UTF-8</propertiesEncoding>
        </configuration>
      </plugin>
      <plugin>
        <groupId>org.apache.maven.plugins</groupId>
        <artifactId>maven-compiler-plugin</artifactId>
        <version>3.14.1</version>
        <configuration>
          <fork>true</fork>
          <executable>${stuntgres.jdk.home}/bin/javac</executable>
          <showWarnings>true</showWarnings>
          <proc>none</proc>
          <compilerArgs>
            <arg>-Xlint:all</arg>
            <arg>-Werror</arg>
          </compilerArgs>
        </configuration>
        <executions>
          <execution>
            <id>default-compile</id>
            <configuration>
              <compilerArgs>
                <arg>-Xlint:all</arg>
                <arg>-Werror</arg>
                <arg>-h</arg>
                <arg>${project.build.directory}/jni</arg>
              </compilerArgs>
            </configuration>
          </execution>
        </executions>
      </plugin>
      <plugin>
        <groupId>org.apache.maven.plugins</groupId>
        <artifactId>maven-surefire-plugin</artifactId>
        <version>3.5.2</version>
        <configuration>
          <jvm>${stuntgres.jdk.home}/bin/java</jvm>
          <groups>${stuntgres.test.groups}</groups>
          <argLine>--enable-native-access=ALL-UNNAMED ${stuntgres.test.jvmArgs}</argLine>
          <forkCount>1</forkCount>
          <reuseForks>true</reuseForks>
          <trimStackTrace>false</trimStackTrace>
          <redirectTestOutputToFile>false</redirectTestOutputToFile>
          <failIfNoSpecifiedTests>true</failIfNoSpecifiedTests>
          <systemPropertyVariables>
            <stuntgres.test.basedir>${project.basedir}</stuntgres.test.basedir>
            <stuntgres.test.builddir>${project.build.directory}</stuntgres.test.builddir>
          </systemPropertyVariables>
        </configuration>
      </plugin>
      <plugin>
        <groupId>org.apache.maven.plugins</groupId>
        <artifactId>maven-jar-plugin</artifactId>
        <version>3.4.2</version>
        <configuration>
          <archive>
            <manifestEntries>
              <Automatic-Module-Name>org.stuntgres.jdbc</Automatic-Module-Name>
              <Implementation-Title>${project.name}</Implementation-Title>
              <Implementation-Version>${project.version}</Implementation-Version>
            </manifestEntries>
          </archive>
        </configuration>
      </plugin>
    </plugins>
  </build>

  <profiles>
    <profile>
      <id>mode-reference</id>
      <activation>
        <property><name>stuntgres.test.mode</name><value>reference</value></property>
      </activation>
      <properties>
        <stuntgres.test.groups>reference</stuntgres.test.groups>
      </properties>
    </profile>
    <profile>
      <id>mode-oracle</id>
      <activation>
        <property><name>stuntgres.test.mode</name><value>oracle</value></property>
      </activation>
      <properties>
        <stuntgres.test.groups>db &amp; !native-only</stuntgres.test.groups>
        <stuntgres.test.jvmArgs>-Xcheck:jni</stuntgres.test.jvmArgs>
      </properties>
    </profile>
    <profile>
      <id>mode-server</id>
      <activation>
        <property><name>stuntgres.test.mode</name><value>server</value></property>
      </activation>
      <properties>
        <stuntgres.test.groups>db &amp; !native-only</stuntgres.test.groups>
        <stuntgres.test.jvmArgs>-Xcheck:jni</stuntgres.test.jvmArgs>
      </properties>
    </profile>
    <profile>
      <id>mode-native</id>
      <activation>
        <property><name>stuntgres.test.mode</name><value>native</value></property>
      </activation>
      <properties>
        <stuntgres.test.groups>db</stuntgres.test.groups>
      </properties>
    </profile>
  </profiles>
</project>
```

Points that matter to later designs:

| Topic | How the pom covers it |
|---|---|
| Version | One place: `<version>`. `org/stuntgres/version.properties` (filtered) carries it to the runtime; the jar manifest carries `Implementation-Version`. |
| Language level | `maven.compiler.release` 17; `-Xlint:all -Werror`; `<proc>none</proc>` (no annotation processing). Main and test sources use the same flags. |
| JDK | `stuntgres.jdk.home`: `javac` is forked from it and the test JVM is started from it. The Makefile sets it to its `JAVA_HOME`; without the Makefile it is the JDK that runs Maven. **(measured: `~/bin/mvn` on the design host exports `JAVA_HOME` of JDK 21 itself, so without this property a run with `JAVA_HOME` of JDK 17 tested on JDK 21; a unit test of BUILD-02 compares the two paths.)** |
| JNI headers | The `default-compile` execution passes `-h target/jni`; a class with a native method or a `@Native` constant gets `target/jni/org_stuntgres_pq_<Class>.h`. |
| Dependencies | `junit-jupiter` 5.12.2 and `org.postgresql:postgresql` 42.7.13, both test scope; pgjdbc's only transitive dependency (`checker-qual`, annotations) is excluded. Nothing at runtime. |
| Resources | `src/main/resources` as is (later: `META-INF/services/java.sql.Driver`); `src/main/resources-filtered` with `${...}` replaced; `target/native-resources`, filled only by `make jar`. |
| Test selection by mode | The profile activated by `-Dstuntgres.test.mode=<mode>` sets the JUnit tag expression: `unit` → `unit`; `reference` → `reference`; `oracle` and `server` → `db & !native-only`; `native` → `db`. |
| Test selection by name | `-Dtest=<selector>` (the Makefile's `TEST`); the tag expression still applies **(measured)**. |
| Facts for the tests | Maven passes every `-D` property of its command line to the forked test JVM **(measured)**; the mode scripts add theirs (10.2). The pom itself adds `stuntgres.test.basedir` and `stuntgres.test.builddir`. |
| Test JVM | One fork for the whole run (`forkCount` 1, `reuseForks` true). Arguments: `--enable-native-access=ALL-UNNAMED` always (accepted by JDK 17 and 21, required to silence JDK 24 and later) and, in oracle and server mode, `-Xcheck:jni`. |
| Plugins | `maven-resources-plugin` 3.3.1, `maven-compiler-plugin` 3.14.1, `maven-surefire-plugin` 3.5.2 (provider `surefire-junit-platform` 3.5.2), `maven-jar-plugin` 3.4.2. All are in `~/.m2` of the design host with their dependencies **(measured: clean build with `mvn -o` on JDK 17 and 21)**. No other plugin runs in `compile`, `test` or `package`. |

What a later design part may need and how it gets it without editing the pom: a new
main or test class, a resource, a `META-INF/services` file and a filtered resource only
need a file in the directories above; a new tag needs no pom change unless a mode has to
exclude it (then the part that introduces it edits the pom and takes an ordering edge to
every pending JDBC part, which has not been necessary in this foundation); native files
in the jar are staged by `make jar`.

### 3.2 `Makefile`

The complete file (BUILD-01 writes it; no later part edits it):

```make
# Build and test the stuntgres JDBC driver (bindings/jdbc).
#
#   make -C bindings/jdbc check MODE=<unit|reference|oracle|server|native> [TEST=<selector>]
#   make -C bindings/jdbc compile | shim | jar [LIBSPQ=<path of libspq.so>] | clean
#
# This file is complete: parts add Java, C and script files, never Makefile lines.
# Sources are found by wildcard; the harness scripts under tools/ are run by name.

SHELL := /bin/bash
.SHELLFLAGS := -eu -o pipefail -c
.DELETE_ON_ERROR:

MODE ?= unit
TEST ?=
LIBSPQ ?=
MVN ?= mvn
CC ?= cc

# The JDK: JAVA_HOME when set, otherwise the one that owns the java on PATH.
ifeq ($(origin JAVA_HOME),undefined)
JAVA_HOME := $(shell j=$$(command -v java) && dirname "$$(dirname "$$(readlink -f "$$j")")")
endif
export JAVA_HOME

MVN_FLAGS := -o -B -Dstyle.color=never -Dorg.slf4j.simpleLogger.defaultLogLevel=warn \
	-Dstuntgres.jdk.home=$(JAVA_HOME)
ARCH := $(shell uname -m)
JNI_HEADERS := target/jni
SHIM := target/native/libstuntgres_jni.so
C_SOURCES := $(sort $(wildcard src/main/c/*.c))
C_HEADERS := $(wildcard src/main/c/*.h)
TEST_C_SOURCES := $(sort $(wildcard src/test/c/*.c))
JAVA_SOURCES := $(shell find src/main/java -name '*.java' 2>/dev/null)
RESOURCES := $(shell find src/main/resources src/main/resources-filtered -type f 2>/dev/null)
CFLAGS_SHIM := -std=c11 -D_GNU_SOURCE -O2 -g -Wall -Wextra -Werror -fPIC -fvisibility=hidden \
	-I$(JAVA_HOME)/include -I$(JAVA_HOME)/include/linux -I$(JNI_HEADERS) -Isrc/main/c
NATIVE_RESOURCES := target/native-resources/META-INF/native/linux-$(ARCH)

.PHONY: jdk compile shim test-natives check jar clean

# Fail early with a clear message when the JDK is missing or older than 17.
jdk:
	@tools/check_jdk.sh "$(JAVA_HOME)"

# Java classes of the driver and the JNI headers (javac -h) under target/jni.
compile: jdk target/compile.stamp

target/compile.stamp: pom.xml $(JAVA_SOURCES) $(RESOURCES)
	$(MVN) $(MVN_FLAGS) compile
	@mkdir -p $(JNI_HEADERS) && touch $@

# The shim. Without C sources (the skeleton) there is nothing to build.
shim: compile $(if $(C_SOURCES),$(SHIM))

$(SHIM): $(C_SOURCES) $(C_HEADERS) target/compile.stamp tools/check_jni.sh
	@mkdir -p $(dir $@)
	$(CC) $(CFLAGS_SHIM) -shared -Wl,--no-undefined -Wl,-soname,libstuntgres_jni.so \
		-o $@ $(C_SOURCES) -ldl -lpthread
	tools/check_jni.sh $(JNI_HEADERS) $@

# Stand-in libraries of the tests, built from src/test/c by the script that the
# owner of those sources provides.
test-natives: shim
	@if [ -n "$(TEST_C_SOURCES)" ]; then \
		CC="$(CC)" tools/build_test_natives.sh "$(ARCH)" $(TEST_C_SOURCES); fi

# Run the tests of one mode. tools/run_mode.sh starts what the mode needs, runs
# Maven with the facts as system properties and removes what it started.
check: shim test-natives
	tools/run_mode.sh "$(MODE)" "$(TEST)" -- $(MVN) $(MVN_FLAGS) test

# The jar with the native files of this platform under META-INF/native/linux-<arch>/.
# LIBSPQ names the libspq.so to include; without it the jar carries only the shim.
jar: shim
	@tools/stage_natives.sh "$(ARCH)" "$(LIBSPQ)"
	$(MVN) $(MVN_FLAGS) package -DskipTests
	@tools/stage_natives.sh --clean

clean:
	rm -rf target
```

| Target | Effect |
|---|---|
| `jdk` | `tools/check_jdk.sh`: exit 1 with one line on `stderr` unless `JAVA_HOME` holds a JDK 17 or newer with `include/jni.h`. |
| `compile` | `mvn -o compile` when a main source, a resource or `pom.xml` is newer than `target/compile.stamp`. Writes `target/classes` and `target/jni/*.h`. |
| `shim` | Compiles every `src/main/c/*.c` into `target/native/libstuntgres_jni.so` when a C file, the header or the stamp is newer; then `tools/check_jni.sh`. With no C file yet (BUILD-01, BUILD-02, PQ-01A, PQ-01B) it does nothing. |
| `test-natives` | Runs `tools/build_test_natives.sh <arch> <sources>` when `src/test/c` has C files (from PQ-02B on). |
| `check` | `shim`, `test-natives`, then `tools/run_mode.sh <MODE> <TEST> -- mvn ... test`. |
| `jar` | Stages the shim (and `LIBSPQ`, when given) under `target/native-resources/META-INF/native/linux-<arch>/` with `natives.sha256`, runs `mvn -o package -DskipTests`, removes the staged files again. Result: `target/stuntgres-jdbc-<version>.jar`. |
| `clean` | Removes `target`. |

Flags of the shim: `-std=c11 -D_GNU_SOURCE -O2 -g -Wall -Wextra -Werror -fPIC
-fvisibility=hidden`, linked `-shared -Wl,--no-undefined -Wl,-soname,libstuntgres_jni.so
-ldl -lpthread`. `--no-undefined` turns a helper that no C file defines into a link
error. `ldd` of the result shows only `libc.so.6` and the dynamic linker **(measured)**.

Maven runs with `-o -B -Dstyle.color=never -Dorg.slf4j.simpleLogger.defaultLogLevel=warn`:
warnings and errors are printed, the `[INFO]` chatter is not. **(measured: with `-q`
instead, a `-Werror` failure printed only "warnings found and -Werror specified" without
the warning itself.)** A skipped test shows as a `[WARNING] Tests run: ..., Skipped: n`
line; a passing run otherwise prints only the commands of the Makefile and the summary
line of `run_mode.sh` (10.1).

**JDK rule.** A run uses the JDK of `JAVA_HOME`; when the variable is not set, the JDK
that owns the `java` on `PATH`. Older than 17:
`bindings/jdbc: JDK 17 or newer is required; <home> is version <v>`; none:
`bindings/jdbc: no JDK found: set JAVA_HOME to a JDK 17 or newer (JAVA_HOME='<value>')`
**(measured with JDK 11.0.29 and a path that does not exist)**. The suite must pass on
JDK 17 and JDK 21; running both is left to a part of JDBC-DESIGN-FEATURES, an
implementer runs the JDK at hand.

**Keeping C and Java in step.** Three mechanisms, all part of `make shim`:

1. Every C file includes the generated header of the class whose native methods it
   defines (`#include "org_stuntgres_pq_ConnExec.h"`); a wrong parameter or return type
   is a compile error (conflicting types).
2. `tools/check_jni.sh` compares the `Java_...` names of all generated headers with the
   symbols the shim exports: a native method without a C function, and an exported
   symbol that no class declares, both fail the build.
3. `src/main/c/sjni_check.c` holds `_Static_assert`s that compare every Java constant
   marked `@Native` with the value in `sjni.h` (4.3).

**Measured times** (JDK 17, 32-core host): clean build with unit run 4.8 s; a unit run
with nothing to rebuild 1.8 s (21 tests, of which the loader tests start about 30 child
JVMs); `make shim` with nothing to do 0.06 s; reference mode 2.4 s, oracle mode 2.7 s,
server mode 1.8 s with the scratch tests (start and removal of the container or server
included); `make jar` 1.3 s.

The three scripts of BUILD-01, complete:

`tools/check_jdk.sh`:

```sh
#!/bin/bash
# check_jdk.sh JAVA_HOME: exit 0 when JAVA_HOME holds a JDK 17 or newer.
set -eu
home=${1:-}
if [ -z "$home" ] || [ ! -x "$home/bin/javac" ] || [ ! -f "$home/include/jni.h" ]; then
    echo "bindings/jdbc: no JDK found: set JAVA_HOME to a JDK 17 or newer (JAVA_HOME='$home')" >&2
    exit 1
fi
version=$("$home/bin/javac" -version 2>&1 | awk '{print $2}')
major=${version%%.*}
if ! [ "$major" -ge 17 ] 2>/dev/null; then
    echo "bindings/jdbc: JDK 17 or newer is required; $home is version $version" >&2
    exit 1
fi
```

`tools/check_jni.sh`:

```sh
#!/bin/bash
# check_jni.sh HEADER_DIR SHIM: every native method that javac declared (the generated
# headers) is defined by the shim, and the shim exports nothing else.
set -eu
headers=$1
shim=$2
declared=$(cat "$headers"/*.h 2>/dev/null | grep -oE 'JNICALL Java_[A-Za-z0-9_]+' | awk '{print $2}' | sort -u)
defined=$(nm -D --defined-only "$shim" | awk '{print $3}' | sort -u)
missing=$(comm -23 <(echo "$declared") <(echo "$defined") | grep -v '^$' || true)
extra=$(comm -13 <(printf '%s\nJNI_OnLoad\n' "$declared" | sort -u) <(echo "$defined") | grep -v '^$' || true)
status=0
if [ -n "$missing" ]; then
    echo "check_jni: native methods without a C function in $shim:" >&2
    echo "$missing" | sed 's/^/  /' >&2
    status=1
fi
if [ -n "$extra" ]; then
    echo "check_jni: symbols exported by $shim that no Java class declares:" >&2
    echo "$extra" | sed 's/^/  /' >&2
    status=1
fi
exit $status
```

`tools/stage_natives.sh`:

```sh
#!/bin/bash
# stage_natives.sh ARCH [LIBSPQ]: put the native files of the jar under
# target/native-resources/META-INF/native/linux-ARCH/ with their checksum list.
# stage_natives.sh --clean: remove the staged files and their copies under
# target/classes, so that later test runs do not see native files on the class path.
set -eu
if [ "$1" = --clean ]; then
    rm -rf target/native-resources target/classes/META-INF/native
    exit 0
fi
arch=$1
libspq=${2:-}
dir=target/native-resources/META-INF/native/linux-$arch
mkdir -p "$dir"
find "$dir" -type f -delete
if [ -f target/native/libstuntgres_jni.so ]; then
    cp target/native/libstuntgres_jni.so "$dir/"
fi
if [ -n "$libspq" ]; then
    cp "$libspq" "$dir/libspq.so"
fi
if ls "$dir" | grep -q '\.so$'; then
    (cd "$dir" && sha256sum -- *.so > natives.sha256)
fi
```

The jar of `make jar` was checked **(measured)**: it holds `META-INF/MANIFEST.MF` with
`Automatic-Module-Name: org.stuntgres.jdbc`, `org/stuntgres/version.properties` with
`version=0.1.0`, the classes, and `META-INF/native/linux-x86_64/libstuntgres_jni.so`,
`natives.sha256` and (with `LIBSPQ=...`) `libspq.so`; a JVM with only that jar on the
class path extracted and loaded the two files (8.3).


## 4. Shim

`libstuntgres_jni.so` is built from `src/main/c/*.c`. It links against the C library
only. It reaches libspq (or, in tests, libpq 18) through function pointers that the
load native method fills with `dlsym`.

Conventions of the C code (they apply to every file of the shim):

- C11, `-Wall -Wextra -Werror`. Each file starts with a one-line comment that names the
  file and its job, includes the C library headers it needs, then `"sjni.h"`, then the
  generated header of the class whose native methods it defines.
- Only `JNIEXPORT` functions are visible; helpers shared between files are declared in
  `sjni.h` (hidden visibility), everything else is `static`.
- A native method that takes a connection handle starts with `sjni_conn_of(env, handle)`
  and returns its failure value when that returns `NULL` (handle 0: `PqException`
  CLOSED is pending).
- After a helper has raised an exception (it returned `NULL` or 0), the function frees
  what it allocated and returns at once: no JNI call is made while an exception is
  pending. The return value in that case is `0`, `NULL` or `JNI_FALSE`.
- Text arrives as a Java `byte[]` and is copied with `sjni_dup_bytes` (NUL-terminated
  `malloc` copy); text returns as a new `byte[]` made by `sjni_new_bytes`. JNI string
  functions (`NewStringUTF`, `GetStringUTFChars`) and critical regions are never used.
- The only Java code called from C: the constructors `PqException(int, byte[])`,
  `PqResult(...)` (6.1) and `PqNotification(int, byte[], byte[])`. Classes and method
  ids are looked up where they are needed, not cached.
- The shim never reads, writes or closes the descriptor a connection reports; it only
  passes it to `poll()`.
- A library function that returns an enum is declared as returning `int`; on the two
  supported ABIs both are returned the same way.

### 4.1 The private header `sjni.h`

The complete file (PQ-02A writes it; no later part edits it):

```c
/*
 * sjni.h - private header of the stuntgres JNI shim (libstuntgres_jni.so).
 *
 * The shim has no link-time dependency on libspq or libpq and includes none of their
 * headers. The types, constants and prototypes below restate the part of the C ABI
 * the shim uses (docs/design/LIBSPQ-CABI.md sections 2 and 3). A real libpq 18 has
 * the same ABI under the symbol prefix "PQ" instead of "SPQ".
 *
 * This file is complete: parts add C files that implement what it declares and
 * never edit it. A later family that needs another library function resolves it
 * with sjni_symbol().
 */
#ifndef SJNI_H
#define SJNI_H

#include <stdatomic.h>
#include <stddef.h>
#include <stdint.h>

#include <jni.h>

/* ---- library types (LIBSPQ-CABI 2.1) ---- */

typedef struct sjni_pg_conn sjni_pgconn;		/* SPGconn / PGconn */
typedef struct sjni_pg_result sjni_pgresult;	/* SPGresult / PGresult */
typedef struct sjni_pg_cancel sjni_pgcancel;	/* SPGcancel / PGcancel */
typedef unsigned int sjni_oid;					/* SPQOid / Oid */

typedef struct sjni_pgnotify					/* SPGnotify / PGnotify */
{
	char	   *relname;
	int			be_pid;
	char	   *extra;
	struct sjni_pgnotify *next;
} sjni_pgnotify;

typedef void (*sjni_notice_receiver) (void *arg, const sjni_pgresult *res);

/* ---- enumerator values (LIBSPQ-CABI 2.2); sjni_check.c ties them to Java ---- */

enum
{
	SJNI_CONNECTION_OK = 0,
	SJNI_CONNECTION_BAD = 1
};

enum
{
	SJNI_POLLING_FAILED = 0,
	SJNI_POLLING_READING = 1,
	SJNI_POLLING_WRITING = 2,
	SJNI_POLLING_OK = 3
};

enum
{
	SJNI_RES_EMPTY_QUERY = 0,
	SJNI_RES_COMMAND_OK = 1,
	SJNI_RES_TUPLES_OK = 2,
	SJNI_RES_COPY_OUT = 3,
	SJNI_RES_COPY_IN = 4,
	SJNI_RES_BAD_RESPONSE = 5,
	SJNI_RES_NONFATAL_ERROR = 6,
	SJNI_RES_FATAL_ERROR = 7,
	SJNI_RES_COPY_BOTH = 8,
	SJNI_RES_SINGLE_TUPLE = 9,
	SJNI_RES_PIPELINE_SYNC = 10,
	SJNI_RES_PIPELINE_ABORTED = 11,
	SJNI_RES_TUPLES_CHUNK = 12
};

enum
{
	SJNI_TRANS_IDLE = 0,
	SJNI_TRANS_ACTIVE = 1,
	SJNI_TRANS_INTRANS = 2,
	SJNI_TRANS_INERROR = 3,
	SJNI_TRANS_UNKNOWN = 4
};

/* The 18 error field codes (LIBSPQ-CABI 2.3), in the order they are packed. */
#define SJNI_DIAG_FIELDS(X) \
	X(SEVERITY, 'S') \
	X(SEVERITY_NONLOCALIZED, 'V') \
	X(SQLSTATE, 'C') \
	X(MESSAGE_PRIMARY, 'M') \
	X(MESSAGE_DETAIL, 'D') \
	X(MESSAGE_HINT, 'H') \
	X(STATEMENT_POSITION, 'P') \
	X(INTERNAL_POSITION, 'p') \
	X(INTERNAL_QUERY, 'q') \
	X(CONTEXT, 'W') \
	X(SCHEMA_NAME, 's') \
	X(TABLE_NAME, 't') \
	X(COLUMN_NAME, 'c') \
	X(DATATYPE_NAME, 'd') \
	X(CONSTRAINT_NAME, 'n') \
	X(SOURCE_FILE, 'F') \
	X(SOURCE_LINE, 'L') \
	X(SOURCE_FUNCTION, 'R')

/* ---- function table ---- */

/*
 * X(member, return type, parameter list). The symbol is <prefix><member>:
 * SPQconnectdb in libspq, PQconnectdb in libpq. Enum results are declared int.
 */
#define SJNI_REQUIRED_FUNCTIONS(X) \
	X(connectStart, sjni_pgconn *, (const char *conninfo)) \
	X(connectPoll, int, (sjni_pgconn *conn)) \
	X(finish, void, (sjni_pgconn *conn)) \
	X(status, int, (const sjni_pgconn *conn)) \
	X(transactionStatus, int, (const sjni_pgconn *conn)) \
	X(errorMessage, char *, (const sjni_pgconn *conn)) \
	X(parameterStatus, const char *, (const sjni_pgconn *conn, const char *paramName)) \
	X(serverVersion, int, (const sjni_pgconn *conn)) \
	X(backendPID, int, (const sjni_pgconn *conn)) \
	X(socket, int, (const sjni_pgconn *conn)) \
	X(setClientEncoding, int, (sjni_pgconn *conn, const char *encoding)) \
	X(libVersion, int, (void)) \
	X(sendQuery, int, (sjni_pgconn *conn, const char *query)) \
	X(getResult, sjni_pgresult *, (sjni_pgconn *conn)) \
	X(consumeInput, int, (sjni_pgconn *conn)) \
	X(exec, sjni_pgresult *, (sjni_pgconn *conn, const char *query)) \
	X(execParams, sjni_pgresult *, (sjni_pgconn *conn, const char *command, int nParams, \
		const sjni_oid *paramTypes, const char *const *paramValues, const int *paramLengths, \
		const int *paramFormats, int resultFormat)) \
	X(prepare, sjni_pgresult *, (sjni_pgconn *conn, const char *stmtName, const char *query, \
		int nParams, const sjni_oid *paramTypes)) \
	X(execPrepared, sjni_pgresult *, (sjni_pgconn *conn, const char *stmtName, int nParams, \
		const char *const *paramValues, const int *paramLengths, const int *paramFormats, \
		int resultFormat)) \
	X(describePrepared, sjni_pgresult *, (sjni_pgconn *conn, const char *stmt)) \
	X(resultStatus, int, (const sjni_pgresult *res)) \
	X(ntuples, int, (const sjni_pgresult *res)) \
	X(nfields, int, (const sjni_pgresult *res)) \
	X(fname, char *, (const sjni_pgresult *res, int field_num)) \
	X(ftable, sjni_oid, (const sjni_pgresult *res, int field_num)) \
	X(ftablecol, int, (const sjni_pgresult *res, int field_num)) \
	X(fformat, int, (const sjni_pgresult *res, int field_num)) \
	X(ftype, sjni_oid, (const sjni_pgresult *res, int field_num)) \
	X(fsize, int, (const sjni_pgresult *res, int field_num)) \
	X(fmod, int, (const sjni_pgresult *res, int field_num)) \
	X(getvalue, char *, (const sjni_pgresult *res, int tup_num, int field_num)) \
	X(getlength, int, (const sjni_pgresult *res, int tup_num, int field_num)) \
	X(getisnull, int, (const sjni_pgresult *res, int tup_num, int field_num)) \
	X(nparams, int, (const sjni_pgresult *res)) \
	X(paramtype, sjni_oid, (const sjni_pgresult *res, int param_num)) \
	X(cmdStatus, char *, (sjni_pgresult *res)) \
	X(cmdTuples, char *, (sjni_pgresult *res)) \
	X(oidValue, sjni_oid, (const sjni_pgresult *res)) \
	X(clear, void, (sjni_pgresult *res)) \
	X(freemem, void, (void *ptr)) \
	X(resultErrorMessage, char *, (const sjni_pgresult *res)) \
	X(resultErrorField, char *, (const sjni_pgresult *res, int fieldcode)) \
	X(setNoticeReceiver, sjni_notice_receiver, (sjni_pgconn *conn, sjni_notice_receiver proc, \
		void *arg)) \
	X(escapeLiteral, char *, (sjni_pgconn *conn, const char *str, size_t len)) \
	X(escapeIdentifier, char *, (sjni_pgconn *conn, const char *str, size_t len)) \
	X(getCancel, sjni_pgcancel *, (sjni_pgconn *conn)) \
	X(cancel, int, (sjni_pgcancel *cancel, char *errbuf, int errbufsize)) \
	X(freeCancel, void, (sjni_pgcancel *cancel)) \
	X(putCopyData, int, (sjni_pgconn *conn, const char *buffer, int nbytes)) \
	X(putCopyEnd, int, (sjni_pgconn *conn, const char *errormsg)) \
	X(getCopyData, int, (sjni_pgconn *conn, char **buffer, int async)) \
	X(notifies, sjni_pgnotify *, (sjni_pgconn *conn))

/*
 * Optional functions, in feature-bit order (bit 0 first): libpq 17 and 18 additions
 * and the SPQX extensions. A missing one leaves its pointer NULL.
 */
#define SJNI_OPTIONAL_FUNCTIONS(X) \
	X(closePrepared, sjni_pgresult *, (sjni_pgconn *conn, const char *stmt)) \
	X(closePortal, sjni_pgresult *, (sjni_pgconn *conn, const char *portal)) \
	X(socketPoll, int, (int sock, int forRead, int forWrite, int64_t end_time)) \
	X(getCurrentTimeUSec, int64_t, (void)) \
	X(setChunkedRowsMode, int, (sjni_pgconn *conn, int chunkSize)) \
	X(Xversion, const char *, (void)) \
	X(XclusterDrop, int, (const char *host, const char *port, char *errbuf, int errbufsize)) \
	X(Xreset, int, (sjni_pgconn *conn)) \
	X(Xdump, int, (sjni_pgconn *conn, char **buffer, size_t *length)) \
	X(Xrestore, int, (sjni_pgconn *conn, const char *buffer, size_t length))

/* Feature bit numbers: SJNI_FEATURE_closePrepared = 0, ... in the order above. */
enum
{
#define X(name, ret, args) SJNI_FEATURE_##name,
	SJNI_OPTIONAL_FUNCTIONS(X)
#undef X
	SJNI_FEATURE_COUNT
};

struct sjni_api
{
#define X(name, ret, args) ret (*name) args;
	SJNI_REQUIRED_FUNCTIONS(X)
	SJNI_OPTIONAL_FUNCTIONS(X)
#undef X
};

/*
 * The functions of the loaded library. Defined in sjni_util.c, filled by the load
 * native method (sjni_load.c); read only after sjni_require_loaded() said yes.
 */
extern struct sjni_api sjni_pq;

/* ---- kinds of org.stuntgres.pq.PqException (tied to Java by sjni_check.c) ---- */

enum
{
	SJNI_KIND_NOT_LOADED = 1,
	SJNI_KIND_LOAD_FAILED = 2,
	SJNI_KIND_UNSUPPORTED_PLATFORM = 3,
	SJNI_KIND_OUT_OF_MEMORY = 4,
	SJNI_KIND_CLOSED = 5,
	SJNI_KIND_BAD_ARGUMENT = 6,
	SJNI_KIND_TOO_LARGE = 7,
	SJNI_KIND_CALL_FAILED = 8
};

/* Largest Java array the shim allocates (bytes or elements). */
#define SJNI_MAX_ARRAY 0x7FFFFFF7

/* ---- connection wrapper ---- */

/* One queued notice: fields_len bytes of packed fields, then message_len bytes. */
typedef struct sjni_notice
{
	struct sjni_notice *next;
	int			fields_len;
	int			message_len;
	char		data[];
} sjni_notice;

/* What a Java connection handle points to. */
typedef struct sjni_conn
{
	sjni_pgconn *pg;			/* never NULL between connect and finish */
	sjni_notice *notice_head;	/* oldest queued notice or NULL */
	sjni_notice *notice_tail;
	int			notice_count;
	int			notice_lost;	/* notices dropped because malloc failed */
} sjni_conn;

/* ---- helpers: sjni_load.c ---- */

/* 1 when a library is loaded. */
int			sjni_is_loaded(void);

/* 1 when loaded; otherwise raises PqException NOT_LOADED and returns 0. */
int			sjni_require_loaded(JNIEnv *env);

/* dlsym of <prefix><name> in the loaded library, NULL when absent or not loaded. */
void	   *sjni_symbol(const char *name);

/* ---- helpers: sjni_util.c ---- */

/* Raise PqException(kind, message). Call only when no exception is pending. */
void		sjni_throw(JNIEnv *env, int kind, const char *fmt,...)
			__attribute__((format(printf, 3, 4)));

/* Raise PqException CALL_FAILED with the connection's error message. */
void		sjni_throw_conn(JNIEnv *env, sjni_conn *conn, const char *what);

/* malloc that raises PqException OUT_OF_MEMORY and returns NULL on failure. */
void	   *sjni_alloc(JNIEnv *env, size_t size);

/*
 * NUL-terminated malloc copy of a Java byte[] (never NULL here); *len gets the
 * length when len is not NULL. NULL with an exception pending on failure.
 */
char	   *sjni_dup_bytes(JNIEnv *env, jbyteArray array, jsize *len);

/* New Java byte[] with a copy of data; NULL with an exception pending on failure. */
jbyteArray	sjni_new_bytes(JNIEnv *env, const char *data, size_t len);

/* The wrapper behind a handle; handle 0 raises PqException CLOSED and returns NULL. */
sjni_conn  *sjni_conn_of(JNIEnv *env, jlong handle);

/*
 * Pack the error fields of res: for every code of SJNI_DIAG_FIELDS that is present,
 * the code byte, the value bytes and one NUL. Returns the size; writes when out is
 * not NULL (out must have room for the size a call with NULL returned).
 */
size_t		sjni_pack_fields(const sjni_pgresult *res, char *out);

/* ---- helpers: sjni_result.c ---- */

/*
 * Turn res into an org.stuntgres.pq.PqResult and clear it. A NULL res raises
 * PqException CALL_FAILED with the connection's message. Returns NULL with an
 * exception pending on failure; res is cleared on every path.
 */
jobject		sjni_result_to_java(JNIEnv *env, sjni_conn *conn, sjni_pgresult *res);

/* ---- helpers: sjni_notice.c ---- */

/* Install the notice receiver of the shim on a fresh connection. */
void		sjni_notice_install(sjni_conn *conn);

/* Free every queued notice (called by finish). */
void		sjni_notice_discard(sjni_conn *conn);

/* ---- helpers: sjni_params.c ---- */

/*
 * Parameter arrays of execParams and execPrepared. sjni_params_load fills p from
 * the Java arrays (types and formats may be NULL); it returns 0 with an exception
 * pending on failure. sjni_params_free releases what load allocated and is safe on
 * a struct that load left half filled.
 */
typedef struct sjni_params
{
	int			count;
	sjni_oid   *types;			/* NULL when the Java array was null */
	const char **values;		/* count pointers; a NULL entry is SQL NULL */
	int		   *lengths;		/* count lengths in bytes */
	int		   *formats;		/* NULL (all text) when the Java array was null */
	char	   *arena;			/* backing store of the values, NUL after each */
} sjni_params;

int			sjni_params_load(JNIEnv *env, jintArray types, jobjectArray values,
							 jintArray formats, sjni_params *p);
void		sjni_params_free(sjni_params *p);

#endif							/* SJNI_H */
```

### 4.2 Loading

`PqLibrary.load0` (declared in 9.4) is the only way a target library gets into the
process. Its C side, `sjni_load.c`, as a numbered algorithm:

1. Copy the arguments: `deps[]` (absolute paths of libraries to open first, may be
   empty), `path` (absolute path of the target library) and `prefix` (`"SPQ"` or `"PQ"`).
2. Lock the shim's mutex. The remaining steps run under it; every failure below unlocks,
   frees the copies and raises `PqException` LOAD_FAILED with the given text.
3. If a library is already loaded by this copy of the shim: when `path` and `prefix`
   equal the loaded ones, return without doing anything; otherwise fail with
   `cannot load <path>: the native library <loaded path> is already loaded`.
4. `path` must start with `/`: else `cannot load <path>: the path is not absolute`.
   `stat(path)` must succeed: else `cannot load <path>: no such file`.
5. Process-wide check for a second copy: walk the loaded objects with `dl_iterate_phdr`;
   an object whose file name (the part after the last `/`) equals that of `path` but
   whose device and inode differ from `path`'s (or which cannot be `stat`ed) is another
   copy: fail with
   `cannot load <path>: another copy is already loaded in this process from <its path>`.
   The same file loaded through another copy of the shim passes this check.
6. Block every signal on the calling thread: `sigfillset`, `pthread_sigmask(SIG_SETMASK)`.
7. `dlopen(dep, RTLD_NOW | RTLD_LOCAL)` for each dependency in order, then
   `dlopen(path, RTLD_NOW | RTLD_LOCAL)`; stop at the first failure and keep
   `dlerror()`'s text.
8. Restore the signal mask.
9. On a failure of step 7: `cannot load <the file that failed>: <dlerror text>`.
10. Resolve every member of `SJNI_REQUIRED_FUNCTIONS` with `dlsym(handle, prefix +
    member)` into a local table. Collect the names of missing ones. Resolve every member
    of `SJNI_OPTIONAL_FUNCTIONS`; a present one sets bit `SJNI_FEATURE_<member>`.
11. If a required symbol is missing: `dlclose(handle)` and fail with
    `cannot use <path>: <n> required symbols are missing: <prefix><member>, ...`
    (the list is cut at 1023 bytes).
12. Publish: copy the local table to `sjni_pq`, remember handle, path, prefix and the
    feature bits, then set the atomic flag that `sjni_is_loaded` reads.

`sjni_symbol(name)` is the door for later families: it returns `dlsym(handle, prefix +
name)` of the loaded library, so a C file added by a later design can use a library
function that is not in the table without editing `sjni.h`.

The complete file `sjni_load.c` (PQ-02C):

```c
/* sjni_load.c - loading the target library and the function table. */
#include <dlfcn.h>
#include <link.h>
#include <pthread.h>
#include <signal.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>

#include "sjni.h"
#include "org_stuntgres_pq_PqLibrary.h"

static pthread_mutex_t load_lock = PTHREAD_MUTEX_INITIALIZER;
static atomic_int loaded;
static void *library;
static char *loaded_path;
static char *loaded_prefix;
static int	feature_bits;

int
sjni_is_loaded(void)
{
	return atomic_load(&loaded);
}

int
sjni_require_loaded(JNIEnv *env)
{
	if (atomic_load(&loaded))
		return 1;
	sjni_throw(env, SJNI_KIND_NOT_LOADED, "the native library is not loaded");
	return 0;
}

static void *
lookup(void *handle, const char *prefix, const char *name)
{
	char		symbol[128];

	snprintf(symbol, sizeof symbol, "%s%s", prefix, name);
	return dlsym(handle, symbol);
}

void *
sjni_symbol(const char *name)
{
	if (!atomic_load(&loaded))
		return NULL;
	return lookup(library, loaded_prefix, name);
}

/* dl_iterate_phdr callback: finds a loaded object with the wanted file name. */
struct same_name
{
	const char *base;			/* file name looked for */
	dev_t		dev;			/* identity of the file about to be loaded */
	ino_t		ino;
	char		other[512];		/* out: path of a different file with that name */
};

static int
find_other_copy(struct dl_phdr_info *info, size_t size, void *data)
{
	struct same_name *q = data;
	const char *name = info->dlpi_name;
	const char *slash;
	struct stat st;

	(void) size;
	if (name == NULL || name[0] == '\0')
		return 0;
	slash = strrchr(name, '/');
	if (strcmp(slash ? slash + 1 : name, q->base) != 0)
		return 0;
	if (stat(name, &st) == 0 && st.st_dev == q->dev && st.st_ino == q->ino)
		return 0;
	snprintf(q->other, sizeof q->other, "%s", name);
	return 1;
}

/* Fill api from handle; returns the number of missing required symbols. */
static int
resolve(void *handle, const char *prefix, struct sjni_api *api, int *features,
		char *missing, size_t missing_size)
{
	int			count = 0;
	size_t		used = 0;

	missing[0] = '\0';
	*features = 0;
#define X(name, ret, args) \
	*(void **) &api->name = lookup(handle, prefix, #name); \
	if (api->name == NULL) \
	{ \
		if (used < missing_size) \
			used += (size_t) snprintf(missing + used, missing_size - used, "%s%s%s", \
									  count ? ", " : "", prefix, #name); \
		count++; \
	}
	SJNI_REQUIRED_FUNCTIONS(X)
#undef X
#define X(name, ret, args) \
	*(void **) &api->name = lookup(handle, prefix, #name); \
	if (api->name != NULL) \
		*features |= 1 << SJNI_FEATURE_##name;
	SJNI_OPTIONAL_FUNCTIONS(X)
#undef X
	return count;
}

/* The load itself; called with load_lock held. On failure fills err and returns 0. */
static int
load_locked(char **deps, int ndeps, const char *path, const char *prefix,
			char *err, size_t errsize)
{
	sigset_t	all,
				old;
	struct stat st;
	struct same_name query;
	struct sjni_api api;
	const char *slash;
	const char *failed = NULL;
	char		reason[512] = "";
	char		missing[1024];
	void	   *handle = NULL;
	int			features;
	int			nmissing;

	if (atomic_load(&loaded))
	{
		if (strcmp(path, loaded_path) == 0 && strcmp(prefix, loaded_prefix) == 0)
			return 1;
		snprintf(err, errsize, "cannot load %s: the native library %s is already loaded",
				 path, loaded_path);
		return 0;
	}
	if (path[0] != '/')
	{
		snprintf(err, errsize, "cannot load %s: the path is not absolute", path);
		return 0;
	}
	if (stat(path, &st) != 0)
	{
		snprintf(err, errsize, "cannot load %s: no such file", path);
		return 0;
	}
	slash = strrchr(path, '/');
	query.base = slash + 1;
	query.dev = st.st_dev;
	query.ino = st.st_ino;
	query.other[0] = '\0';
	if (dl_iterate_phdr(find_other_copy, &query))
	{
		snprintf(err, errsize,
				 "cannot load %s: another copy is already loaded in this process from %s",
				 path, query.other);
		return 0;
	}

	sigfillset(&all);
	pthread_sigmask(SIG_SETMASK, &all, &old);
	for (int i = 0; i < ndeps && failed == NULL; i++)
		if (dlopen(deps[i], RTLD_NOW | RTLD_LOCAL) == NULL)
			failed = deps[i];
	if (failed == NULL && (handle = dlopen(path, RTLD_NOW | RTLD_LOCAL)) == NULL)
		failed = path;
	if (failed != NULL)
		snprintf(reason, sizeof reason, "%s", dlerror());
	pthread_sigmask(SIG_SETMASK, &old, NULL);
	if (failed != NULL)
	{
		snprintf(err, errsize, "cannot load %s: %s", failed, reason);
		return 0;
	}

	nmissing = resolve(handle, prefix, &api, &features, missing, sizeof missing);
	if (nmissing > 0)
	{
		dlclose(handle);
		snprintf(err, errsize, "cannot use %s: %d required symbols are missing: %s",
				 path, nmissing, missing);
		return 0;
	}
	loaded_path = strdup(path);
	loaded_prefix = strdup(prefix);
	if (loaded_path == NULL || loaded_prefix == NULL)
	{
		snprintf(err, errsize, "cannot load %s: out of memory", path);
		return 0;
	}
	sjni_pq = api;
	feature_bits = features;
	library = handle;
	atomic_store(&loaded, 1);
	return 1;
}

JNIEXPORT void JNICALL
Java_org_stuntgres_pq_PqLibrary_load0(JNIEnv *env, jclass cls, jobjectArray jdeps,
									  jbyteArray jpath, jbyteArray jprefix)
{
	char		err[2048] = "";
	char	   *path = NULL;
	char	   *prefix = NULL;
	char	  **deps = NULL;
	int			ndeps = (*env)->GetArrayLength(env, jdeps);
	int			ok = 0;

	(void) cls;
	deps = calloc((size_t) ndeps + 1, sizeof *deps);
	if (deps == NULL)
	{
		sjni_throw(env, SJNI_KIND_OUT_OF_MEMORY, "out of memory in the native shim");
		return;
	}
	for (int i = 0; i < ndeps; i++)
	{
		jbyteArray	item = (jbyteArray) (*env)->GetObjectArrayElement(env, jdeps, i);

		deps[i] = sjni_dup_bytes(env, item, NULL);
		(*env)->DeleteLocalRef(env, item);
		if (deps[i] == NULL)
			goto done;
	}
	if ((path = sjni_dup_bytes(env, jpath, NULL)) == NULL)
		goto done;
	if ((prefix = sjni_dup_bytes(env, jprefix, NULL)) == NULL)
		goto done;

	pthread_mutex_lock(&load_lock);
	ok = load_locked(deps, ndeps, path, prefix, err, sizeof err);
	pthread_mutex_unlock(&load_lock);
	if (!ok)
		sjni_throw(env, SJNI_KIND_LOAD_FAILED, "%s", err);

done:
	for (int i = 0; i < ndeps; i++)
		free(deps[i]);
	free(deps);
	free(path);
	free(prefix);
}

JNIEXPORT jint JNICALL
Java_org_stuntgres_pq_PqLibrary_features0(JNIEnv *env, jclass cls)
{
	(void) cls;
	if (!sjni_require_loaded(env))
		return 0;
	return feature_bits;
}

JNIEXPORT jint JNICALL
Java_org_stuntgres_pq_PqLibrary_libVersion0(JNIEnv *env, jclass cls)
{
	(void) cls;
	if (!sjni_require_loaded(env))
		return 0;
	return sjni_pq.libVersion();
}

JNIEXPORT jint JNICALL
JNI_OnLoad(JavaVM *vm, void *reserved)
{
	(void) vm;
	(void) reserved;
	return JNI_VERSION_1_8;
}
```

Measured behavior of this design **(experiment 2, both JDKs, under `-Xcheck:jni`)**:

| Case | Observation |
|---|---|
| libpq 18 with its two dependencies first, prefix `PQ` | loaded; `libVersion` 180006; all five libpq optional functions present, the five `X` extensions absent |
| libpq 18 without the dependencies | `cannot load <path>/libpq.so.5.18: libldap-2.5.so.0: cannot open shared object file: No such file or directory` |
| the host's libpq 16.15 | loaded; `libVersion` 160015; no optional function: every feature flag is false |
| a Go c-shared library with 24 of the required symbols (the libspq validation slice), prefix `SPQ` | `cannot use <path>: 32 required symbols are missing: SPQconnectStart, SPQconnectPoll, SPQtransactionStatus, ...`; the Go runtime had started and the JVM kept working |
| libpq with prefix `SPQ` | `cannot use <path>: 52 required symbols are missing: ...` |
| relative path; missing file | `cannot load libpq.so.5: the path is not absolute`; `cannot load /nonexistent/libspq.so: no such file` |
| a failed load, then a correct one in the same JVM | the second succeeds (nothing is latched by a failure) |
| `ensureLoaded` again after a success, also with the properties now naming another library | returns at once; the loaded library stays |
| `/proc/self/maps` after initializing every class of `org.stuntgres.pq` | no shim, no target library; after the first `ensureLoaded`: the shim, the target and its dependencies |

Flags: `RTLD_NOW` reports a library that cannot be bound completely at load time
instead of at the first call; `RTLD_LOCAL` keeps the target's symbols out of the global
scope, so a libpq that another native library of the application uses is not affected.
The dependencies are found without `LD_LIBRARY_PATH` because the dynamic linker
satisfies a `DT_NEEDED` entry from an object that is already loaded under the same
soname.

Two class loaders in one JVM (application servers, test runners): the JDK refuses to
load one shim file in two loaders,
`java.lang.UnsatisfiedLinkError: Native Library <path> already loaded in another
classloader` **(measured, JDK 17 and 21)**. The loader therefore gives the second class
loader a private copy of the shim file (8.2). Each copy has its own function table and
calls `dlopen` for the same target path; the dynamic linker returns the object that is
already mapped, so the target library exists once **(measured: two class loaders, both
loaded, `/proc/self/maps` shows one `libpq.so.5.18` and two shims)**. A second class
loader that asks for a different file of the same name is refused by step 5
**(measured)**. Two copies of a Go library in one JVM did not crash in a 20 s run
**(measured)**; the check exists because docs/LIBSPQ.md section 7 rule 2 demands one
copy: a second copy would have its own clusters.

### 4.3 Helpers, the function table and the constant checks

`sjni_util.c` (PQ-02B) defines the table `sjni_pq` and the helpers every other file
uses. The complete file:

```c
/* sjni_util.c - helpers shared by every file of the shim. */
#include <stdarg.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "sjni.h"

struct sjni_api sjni_pq;

void
sjni_throw(JNIEnv *env, int kind, const char *fmt,...)
{
	char		text[2048];
	va_list		ap;
	jclass		cls;
	jmethodID	ctor;
	jbyteArray	bytes;
	jobject		exc;
	int			n;

	va_start(ap, fmt);
	n = vsnprintf(text, sizeof text, fmt, ap);
	va_end(ap);
	if (n < 0)
		n = 0;
	if ((size_t) n >= sizeof text)
		n = (int) sizeof text - 1;
	while (n > 0 && text[n - 1] == '\n')
		n--;

	cls = (*env)->FindClass(env, "org/stuntgres/pq/PqException");
	if (cls == NULL)
		return;
	ctor = (*env)->GetMethodID(env, cls, "<init>", "(I[B)V");
	if (ctor == NULL)
		return;
	bytes = (*env)->NewByteArray(env, n);
	if (bytes == NULL)
		return;
	(*env)->SetByteArrayRegion(env, bytes, 0, n, (const jbyte *) text);
	exc = (*env)->NewObject(env, cls, ctor, (jint) kind, bytes);
	if (exc != NULL)
		(*env)->Throw(env, (jthrowable) exc);
}

void
sjni_throw_conn(JNIEnv *env, sjni_conn *conn, const char *what)
{
	const char *message = sjni_pq.errorMessage(conn->pg);

	if (message == NULL || message[0] == '\0')
		message = "no error message";
	sjni_throw(env, SJNI_KIND_CALL_FAILED, "%s failed: %s", what, message);
}

void *
sjni_alloc(JNIEnv *env, size_t size)
{
	void	   *p = malloc(size ? size : 1);

	if (p == NULL)
		sjni_throw(env, SJNI_KIND_OUT_OF_MEMORY,
				   "out of memory in the native shim (%zu bytes)", size);
	return p;
}

char *
sjni_dup_bytes(JNIEnv *env, jbyteArray array, jsize *len)
{
	jsize		n = (*env)->GetArrayLength(env, array);
	char	   *copy = sjni_alloc(env, (size_t) n + 1);

	if (copy == NULL)
		return NULL;
	(*env)->GetByteArrayRegion(env, array, 0, n, (jbyte *) copy);
	copy[n] = '\0';
	if (len != NULL)
		*len = n;
	return copy;
}

jbyteArray
sjni_new_bytes(JNIEnv *env, const char *data, size_t len)
{
	jbyteArray	array;

	if (len > SJNI_MAX_ARRAY)
	{
		sjni_throw(env, SJNI_KIND_TOO_LARGE,
				   "a value of %zu bytes does not fit in a Java array", len);
		return NULL;
	}
	array = (*env)->NewByteArray(env, (jsize) len);
	if (array == NULL)
		return NULL;
	if (len > 0)
		(*env)->SetByteArrayRegion(env, array, 0, (jsize) len, (const jbyte *) data);
	return array;
}

sjni_conn *
sjni_conn_of(JNIEnv *env, jlong handle)
{
	if (handle == 0)
	{
		sjni_throw(env, SJNI_KIND_CLOSED, "the connection is closed");
		return NULL;
	}
	return (sjni_conn *) (intptr_t) handle;
}

size_t
sjni_pack_fields(const sjni_pgresult *res, char *out)
{
	static const char codes[] = {
#define X(name, code) code,
		SJNI_DIAG_FIELDS(X)
#undef X
	};
	size_t		used = 0;

	for (size_t i = 0; i < sizeof codes; i++)
	{
		const char *value = sjni_pq.resultErrorField(res, codes[i]);
		size_t		n;

		if (value == NULL)
			continue;
		n = strlen(value);
		if (out != NULL)
		{
			out[used] = codes[i];
			memcpy(out + used + 1, value, n + 1);
		}
		used += n + 2;
	}
	return used;
}
```

`sjni_check.c` (PQ-02A) ties the header to the Java constants; a mismatch stops the
build. The complete file:

```c
/* sjni_check.c - compile-time ties between sjni.h and the Java constants. */
#include "sjni.h"
#include "org_stuntgres_pq_ConnStatus.h"
#include "org_stuntgres_pq_DiagField.h"
#include "org_stuntgres_pq_ExecStatus.h"
#include "org_stuntgres_pq_Feature.h"
#include "org_stuntgres_pq_PqException.h"
#include "org_stuntgres_pq_TransactionStatus.h"

#define SAME(java, c) _Static_assert((java) == (c), #java " differs from " #c)

SAME(org_stuntgres_pq_ConnStatus_OK, SJNI_CONNECTION_OK);
SAME(org_stuntgres_pq_ConnStatus_BAD, SJNI_CONNECTION_BAD);

SAME(org_stuntgres_pq_ExecStatus_EMPTY_QUERY, SJNI_RES_EMPTY_QUERY);
SAME(org_stuntgres_pq_ExecStatus_COMMAND_OK, SJNI_RES_COMMAND_OK);
SAME(org_stuntgres_pq_ExecStatus_TUPLES_OK, SJNI_RES_TUPLES_OK);
SAME(org_stuntgres_pq_ExecStatus_COPY_OUT, SJNI_RES_COPY_OUT);
SAME(org_stuntgres_pq_ExecStatus_COPY_IN, SJNI_RES_COPY_IN);
SAME(org_stuntgres_pq_ExecStatus_BAD_RESPONSE, SJNI_RES_BAD_RESPONSE);
SAME(org_stuntgres_pq_ExecStatus_NONFATAL_ERROR, SJNI_RES_NONFATAL_ERROR);
SAME(org_stuntgres_pq_ExecStatus_FATAL_ERROR, SJNI_RES_FATAL_ERROR);
SAME(org_stuntgres_pq_ExecStatus_COPY_BOTH, SJNI_RES_COPY_BOTH);
SAME(org_stuntgres_pq_ExecStatus_SINGLE_TUPLE, SJNI_RES_SINGLE_TUPLE);
SAME(org_stuntgres_pq_ExecStatus_PIPELINE_SYNC, SJNI_RES_PIPELINE_SYNC);
SAME(org_stuntgres_pq_ExecStatus_PIPELINE_ABORTED, SJNI_RES_PIPELINE_ABORTED);
SAME(org_stuntgres_pq_ExecStatus_TUPLES_CHUNK, SJNI_RES_TUPLES_CHUNK);

SAME(org_stuntgres_pq_TransactionStatus_IDLE, SJNI_TRANS_IDLE);
SAME(org_stuntgres_pq_TransactionStatus_ACTIVE, SJNI_TRANS_ACTIVE);
SAME(org_stuntgres_pq_TransactionStatus_INTRANS, SJNI_TRANS_INTRANS);
SAME(org_stuntgres_pq_TransactionStatus_INERROR, SJNI_TRANS_INERROR);
SAME(org_stuntgres_pq_TransactionStatus_UNKNOWN, SJNI_TRANS_UNKNOWN);

#define X(name, code) SAME(org_stuntgres_pq_DiagField_##name, code);
SJNI_DIAG_FIELDS(X)
#undef X

SAME(org_stuntgres_pq_Feature_CLOSE_PREPARED, SJNI_FEATURE_closePrepared);
SAME(org_stuntgres_pq_Feature_CLOSE_PORTAL, SJNI_FEATURE_closePortal);
SAME(org_stuntgres_pq_Feature_SOCKET_POLL, SJNI_FEATURE_socketPoll);
SAME(org_stuntgres_pq_Feature_CURRENT_TIME_USEC, SJNI_FEATURE_getCurrentTimeUSec);
SAME(org_stuntgres_pq_Feature_CHUNKED_ROWS, SJNI_FEATURE_setChunkedRowsMode);
SAME(org_stuntgres_pq_Feature_EXT_VERSION, SJNI_FEATURE_Xversion);
SAME(org_stuntgres_pq_Feature_EXT_CLUSTER_DROP, SJNI_FEATURE_XclusterDrop);
SAME(org_stuntgres_pq_Feature_EXT_RESET, SJNI_FEATURE_Xreset);
SAME(org_stuntgres_pq_Feature_EXT_DUMP, SJNI_FEATURE_Xdump);
SAME(org_stuntgres_pq_Feature_EXT_RESTORE, SJNI_FEATURE_Xrestore);
SAME(org_stuntgres_pq_Feature_COUNT, SJNI_FEATURE_COUNT);

SAME(org_stuntgres_pq_PqException_NOT_LOADED, SJNI_KIND_NOT_LOADED);
SAME(org_stuntgres_pq_PqException_LOAD_FAILED, SJNI_KIND_LOAD_FAILED);
SAME(org_stuntgres_pq_PqException_UNSUPPORTED_PLATFORM, SJNI_KIND_UNSUPPORTED_PLATFORM);
SAME(org_stuntgres_pq_PqException_OUT_OF_MEMORY, SJNI_KIND_OUT_OF_MEMORY);
SAME(org_stuntgres_pq_PqException_CLOSED, SJNI_KIND_CLOSED);
SAME(org_stuntgres_pq_PqException_BAD_ARGUMENT, SJNI_KIND_BAD_ARGUMENT);
SAME(org_stuntgres_pq_PqException_TOO_LARGE, SJNI_KIND_TOO_LARGE);
SAME(org_stuntgres_pq_PqException_CALL_FAILED, SJNI_KIND_CALL_FAILED);
```

**Function table.** 52 required and 10 optional members; the symbol is the prefix
followed by the member name.

| Family | Required members |
|---|---|
| Connect | `connectStart`, `connectPoll`, `finish` |
| Status | `status`, `transactionStatus`, `errorMessage`, `parameterStatus`, `serverVersion`, `backendPID`, `socket`, `setClientEncoding`, `libVersion` |
| Execution | `sendQuery`, `getResult`, `consumeInput`, `exec`, `execParams`, `prepare`, `execPrepared`, `describePrepared` |
| Results | `resultStatus`, `ntuples`, `nfields`, `fname`, `ftable`, `ftablecol`, `fformat`, `ftype`, `fsize`, `fmod`, `getvalue`, `getlength`, `getisnull`, `nparams`, `paramtype`, `cmdStatus`, `cmdTuples`, `oidValue`, `clear`, `freemem` |
| Errors and notices | `resultErrorMessage`, `resultErrorField`, `setNoticeReceiver` |
| Escaping | `escapeLiteral`, `escapeIdentifier` |
| Cancel | `getCancel`, `cancel`, `freeCancel` |
| COPY | `putCopyData`, `putCopyEnd`, `getCopyData` |
| Notifications | `notifies` |

| Feature bit | Optional member | `Feature` constant | In libpq 18 | In libspq | Native method that uses it |
|---|---|---|---|---|---|
| 0 | `closePrepared` | `CLOSE_PREPARED` | yes (since 17) | yes | `ConnPrepared.closePrepared0` |
| 1 | `closePortal` | `CLOSE_PORTAL` | yes (since 17) | yes | none in this foundation |
| 2 | `socketPoll` | `SOCKET_POLL` | yes (since 17) | yes | none: the shim polls the descriptor itself |
| 3 | `getCurrentTimeUSec` | `CURRENT_TIME_USEC` | yes (since 18) | yes | none |
| 4 | `setChunkedRowsMode` | `CHUNKED_ROWS` | yes (since 17) | yes | none (plan J11: results are fetched completely) |
| 5 | `Xversion` | `EXT_VERSION` | no | yes | `PqCluster.version0` |
| 6 | `XclusterDrop` | `EXT_CLUSTER_DROP` | no | yes | `PqCluster.clusterDrop0` |
| 7 | `Xreset` | `EXT_RESET` | no | yes | `PqExt.reset0` |
| 8 | `Xdump` | `EXT_DUMP` | no | yes | `PqExt.dump0` |
| 9 | `Xrestore` | `EXT_RESTORE` | no | yes | `PqExt.restore0` |

Java never asks which library is loaded and never looks at `libVersion` to decide
anything; it asks `PqLibrary.hasFeature(Feature.X)`. The four optional members without a
native method are resolved so that their flags exist and a later family can call them
through `sjni_pq` without a header change.

The functions of docs/LIBSPQ.md section 3 that are not in the table are listed with
the reasons in 5.9.


## 5. Native methods

There are 35 native methods, all `private static native` (or package-private where a
second class of the package calls them) in classes of `org.stuntgres.pq`. Their names
end in `0`. The C function of `Class.method0` is `Java_org_stuntgres_pq_Class_method0`
with the parameters `(JNIEnv *env, jclass cls, ...)`; `jclass cls` is unused.

Rules that hold for every row of the tables below:

- **Arguments.** A `byte[]` argument stays owned by Java; C copies it (`sjni_dup_bytes`,
  `GetByteArrayRegion`). Text arguments are UTF-8 without a zero byte (Java checks,
  9.3). `long handle` is the address of the shim's `sjni_conn` (7.1); `long cancel` is
  the library's cancel object.
- **Results.** Every returned object or array is new and owned by the caller. Memory
  obtained from the library is released before the function returns, on every path
  (`clear` for results, `freemem` for buffers).
- **Handle 0.** `sjni_conn_of` raises `PqException` CLOSED, text
  `the connection is closed`, and the function returns its failure value. Java never
  passes 0 (`ConnBase.handle()` raises the same exception first); the C check is the
  second line of defence.
- **Library not loaded.** Functions without a handle call `sjni_require_loaded`, which
  raises `PqException` NOT_LOADED, text `the native library is not loaded`. A handle
  cannot exist without a loaded library.
- **Bad connection** (the session was lost or ended). The library function fails. A
  function that returns a result then gets `NULL` and raises CALL_FAILED with the
  connection's message (`sjni_result_to_java`, 6.2); a function that returns an `int`
  passes the library's failure value on and the Java method raises CALL_FAILED (9.5).
  **(measured after `pg_terminate_backend`: the first `exec` returned a FATAL_ERROR
  result without fields and the message `server closed the connection unexpectedly` ...,
  status became BAD; the second raised CALL_FAILED `the command failed: no connection to
  the server`; `sendQuery` raised CALL_FAILED `sending the statement failed: no
  connection to the server`.)**
- **Exceptions.** A native method returns with at most one pending exception and has
  made no JNI call after raising it. The exceptions are `PqException` (kinds in 9.3) and
  the `OutOfMemoryError` the JVM raises when it cannot allocate an array or object.
- **Threads.** A connection handle is used by one thread at a time; `PqCancel.cancel0`
  is the only function that may run while another call on the same connection is in
  progress (it touches only the cancel object).

### 5.1 The table

`conn` below stands for the `sjni_pgconn *` of the handle.

| # | Java declaration | Library functions | Returns | Failure value; exceptions |
|---|---|---|---|---|
| 1 | `PqLibrary`: `private static native void load0(byte[][] dependencies, byte[] path, byte[] prefix) throws PqException` | `dlopen`, `dlsym` (4.2) | nothing | LOAD_FAILED, OUT_OF_MEMORY |
| 2 | `PqLibrary`: `private static native int features0() throws PqException` | none | the feature bits (4.3) | 0; NOT_LOADED |
| 3 | `PqLibrary`: `private static native int libVersion0() throws PqException` | `libVersion` | its value | 0; NOT_LOADED |
| 4 | `ConnBase`: `private static native long connect0(byte[] conninfo, long timeoutMillis) throws PqException` | `connectStart`, `setNoticeReceiver`, `status`, `connectPoll`, `socket`, `finish` | a handle; the connection may have status BAD (Java checks) | 0; NOT_LOADED, OUT_OF_MEMORY, CALL_FAILED `timeout expired` |
| 5 | `ConnBase`: `private static native void finish0(long handle)` | `finish` | nothing; frees the queue and the wrapper; handle 0 is ignored | none |
| 6 | `ConnBase`: `private static native int status0(long handle) throws PqException` | `status` | `ConnStatus` value | 1 (BAD); CLOSED |
| 7 | `ConnBase`: `private static native byte[] errorMessage0(long handle) throws PqException` | `errorMessage` | the message bytes, possibly empty | null; CLOSED |
| 8 | `ConnSession`: `private static native int transactionStatus0(long handle) throws PqException` | `transactionStatus` | `TransactionStatus` value | 4 (UNKNOWN); CLOSED |
| 9 | `ConnSession`: `private static native byte[] parameterStatus0(long handle, byte[] name) throws PqException` | `parameterStatus` | the value bytes, or null when the server reports no such parameter | null; CLOSED |
| 10 | `ConnSession`: `private static native int serverVersion0(long handle) throws PqException` | `serverVersion` | for example 180006 | 0; CLOSED |
| 11 | `ConnSession`: `private static native int backendPid0(long handle) throws PqException` | `backendPID` | the pid | 0; CLOSED |
| 12 | `ResultCopy`: `static native PqResult exec0(long handle, byte[] sql) throws PqException` | `exec`, the result accessors, `clear` | the last result of the string (6.2) | null; CLOSED, CALL_FAILED, TOO_LARGE, OUT_OF_MEMORY |
| 13 | `ConnExec`: `private static native boolean sendQuery0(long handle, byte[] sql) throws PqException` | `sendQuery` | true when the library accepted the command | false; CLOSED |
| 14 | `ConnExec`: `private static native PqResult getResult0(long handle) throws PqException` | `getResult`, the result accessors, `clear` | the next result, or null when the command is complete | null; CLOSED, TOO_LARGE, OUT_OF_MEMORY |
| 15 | `ConnParams`: `private static native PqResult execParams0(long handle, byte[] sql, int[] paramTypes, byte[][] paramValues, int[] paramFormats) throws PqException` | `execParams` (result format 0), accessors, `clear` | the result | as 12 |
| 16 | `ConnPrepared`: `private static native PqResult prepare0(long handle, byte[] name, byte[] sql, int[] paramTypes) throws PqException` | `prepare`, accessors, `clear` | COMMAND_OK or an error result | as 12 |
| 17 | `ConnPrepared`: `private static native PqResult execPrepared0(long handle, byte[] name, byte[][] paramValues, int[] paramFormats) throws PqException` | `execPrepared` (result format 0), accessors, `clear` | the result | as 12 |
| 18 | `ConnPrepared`: `private static native PqResult describePrepared0(long handle, byte[] name) throws PqException` | `describePrepared`, accessors, `clear` | a result with fields and parameter types, no rows | as 12 |
| 19 | `ConnPrepared`: `private static native PqResult closePrepared0(long handle, byte[] name) throws PqException` | `closePrepared` (optional), accessors, `clear` | COMMAND_OK | as 12; CALL_FAILED `the loaded library has no closePrepared` (Java checks the feature first) |
| 20 | `ConnNotices`: `private static native byte[][] drainNotices0(long handle) throws PqException` | none (reads the queue of 7.2) | null when the queue is empty; else an array of 2n elements: packed fields and message of each notice, oldest first | null; CLOSED |
| 21 | `PqCancel`: `private static native long getCancel0(long connection) throws PqException` | `getCancel` | the cancel object | 0; CLOSED, CALL_FAILED |
| 22 | `PqCancel`: `private static native byte[] cancel0(long cancel)` | `cancel` | null on success; else the bytes of the library's error buffer | none |
| 23 | `PqCancel`: `private static native void freeCancel0(long cancel)` | `freeCancel` | nothing; 0 is ignored | none |
| 24 | `ConnNotify`: `private static native PqNotification[] notifies0(long handle, long timeoutMillis) throws PqException` | `consumeInput`, `notifies`, `socket`, `freemem` | the notifications that arrived, possibly an empty array | null; CLOSED, CALL_FAILED |
| 25 | `ConnCopy`: `private static native int putCopyData0(long handle, byte[] buffer, int offset, int length) throws PqException` | `putCopyData` | the library's value: 1 queued, -1 error | -1; CLOSED, OUT_OF_MEMORY |
| 26 | `ConnCopy`: `private static native int putCopyEnd0(long handle, byte[] errorMessage) throws PqException` | `putCopyEnd` | 1 or -1; `errorMessage` null completes the copy | -1; CLOSED |
| 27 | `ConnCopy`: `private static native byte[] getCopyData0(long handle) throws PqException` | `getCopyData` (blocking), `freemem` | one row; null when the copy is done | null; CLOSED, CALL_FAILED |
| 28 | `ConnEscape`: `private static native byte[] escapeLiteral0(long handle, byte[] text) throws PqException` | `escapeLiteral`, `freemem` | the literal with quotes | null; CLOSED, CALL_FAILED |
| 29 | `ConnEscape`: `private static native byte[] escapeIdentifier0(long handle, byte[] text) throws PqException` | `escapeIdentifier`, `freemem` | the identifier with quotes | null; CLOSED, CALL_FAILED |
| 30 | `ConnEscape`: `private static native int setClientEncoding0(long handle, byte[] name) throws PqException` | `setClientEncoding` | 0 on success, -1 on failure | -1; CLOSED |
| 31 | `PqCluster`: `private static native byte[] version0() throws PqException` | `Xversion` | the version bytes | null; NOT_LOADED |
| 32 | `PqCluster`: `private static native byte[] clusterDrop0(byte[] host, byte[] port) throws PqException` | `XclusterDrop` | null on success; else the error text | null; NOT_LOADED |
| 33 | `PqExt`: `private static native boolean reset0(long handle) throws PqException` | `Xreset` | true on success | false; CLOSED |
| 34 | `PqExt`: `private static native byte[] dump0(long handle) throws PqException` | `Xdump`, `freemem` | the dump; null on failure | null; CLOSED, TOO_LARGE |
| 35 | `PqExt`: `private static native boolean restore0(long handle, byte[] dump) throws PqException` | `Xrestore` | true on success | false; CLOSED |

Rows 31 to 35 call optional functions without a C-side check: their Java callers call
`PqLibrary.require(feature)` first (9.4), which raises UNSUPPORTED.

The C prototypes are those `javac -h` writes; for example row 15:

```c
JNIEXPORT jobject JNICALL
Java_org_stuntgres_pq_ConnParams_execParams0(JNIEnv *env, jclass cls, jlong handle,
                                             jbyteArray jsql, jintArray jtypes,
                                             jobjectArray jvalues, jintArray jformats);
```

Type mapping: `long` → `jlong`, `int` → `jint`, `boolean` → `jboolean`, `byte[]` →
`jbyteArray`, `int[]` → `jintArray`, `byte[][]` and `PqNotification[]` → `jobjectArray`,
`PqResult` → `jobject`. Each complete C file below shows the exact prototypes.

### 5.2 Connect, finish, status (`sjni_conn.c`, PQ-04A)

`connect0(conninfo, timeoutMillis)`:

1. `sjni_require_loaded`; copy `conninfo`; allocate a zeroed `sjni_conn`.
2. `conn->pg = connectStart(conninfo)`. `NULL`: free the wrapper, raise OUT_OF_MEMORY
   `out of memory: the library returned no connection object`.
3. `sjni_notice_install(conn)`: the receiver is in place before any message of the
   server is read.
4. If `status(conn->pg)` is not BAD, drive the connection (`finish_connect`): start as
   if the last poll had asked for writing; loop: compute the time left (when a limit is
   given; none left: give up); `poll()` the descriptor of `socket(conn->pg)` for writing
   or reading as the last poll asked; on a time-out or `EINTR` go round again;
   otherwise `state = connectPoll(conn->pg)`; stop when it is `SJNI_POLLING_OK` or
   `SJNI_POLLING_FAILED`. **`connectPoll` is called only after `poll()` reported the
   descriptor ready** (measured: called on a descriptor that is not ready, libpq takes a
   TCP connect still in progress for an established one and blocks in `send` for the
   kernel's connect time-out, 130 s).
5. Gave up: `finish`, free queue and wrapper, raise CALL_FAILED `timeout expired`
   (`ConnBase.open` turns it into CONNECT_FAILED).
6. Return the wrapper's address. A failed attempt (status BAD) returns a handle too:
   Java reads the message, calls `finish0` and raises CONNECT_FAILED (9.5).

With libspq `connectStart` completes the connection and `connectPoll` reports OK at
once; the descriptor is always writable (docs/LIBSPQ.md 5.1), so the same loop works.

`finish0(handle)`: ignore 0; `finish(conn->pg)`; `sjni_notice_discard(conn)`; free the
wrapper. `status0`, `errorMessage0`: one accessor call each; a `NULL` message becomes an
empty array.

The complete file:

```c
/* sjni_conn.c - connect, finish and the session accessors. */
#include <errno.h>
#include <poll.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

#include "sjni.h"
#include "org_stuntgres_pq_ConnBase.h"

static int64_t
now_ms(void)
{
	struct timespec ts;

	clock_gettime(CLOCK_MONOTONIC, &ts);
	return (int64_t) ts.tv_sec * 1000 + ts.tv_nsec / 1000000;
}

/*
 * Drive a started connection to its end, as the libpq manual prescribes: wait until
 * the socket is ready for what the last poll asked for (writing at first), then
 * poll again. connectPoll is never called on a socket that is not ready: libpq
 * would take a connect still in progress for an established one and block.
 * Returns 0 when the time ran out.
 */
static int
finish_connect(sjni_conn *conn, int64_t timeout_ms)
{
	int64_t		deadline = timeout_ms > 0 ? now_ms() + timeout_ms : 0;
	int			state = SJNI_POLLING_WRITING;

	for (;;)
	{
		struct pollfd p;
		int64_t		left = -1;
		int			rc;

		if (timeout_ms > 0 && (left = deadline - now_ms()) <= 0)
			return 0;
		p.fd = sjni_pq.socket(conn->pg);
		p.events = state == SJNI_POLLING_READING ? POLLIN : POLLOUT;
		p.revents = 0;
		rc = poll(&p, 1, left > 0x7FFFFFFF ? 0x7FFFFFFF : (int) left);
		if (rc == 0 || (rc < 0 && errno == EINTR))
			continue;
		state = sjni_pq.connectPoll(conn->pg);
		if (state == SJNI_POLLING_OK || state == SJNI_POLLING_FAILED)
			return 1;
	}
}

JNIEXPORT jlong JNICALL
Java_org_stuntgres_pq_ConnBase_connect0(JNIEnv *env, jclass cls, jbyteArray jconninfo,
										jlong timeout_ms)
{
	char	   *conninfo;
	sjni_conn  *conn;

	(void) cls;
	if (!sjni_require_loaded(env))
		return 0;
	if ((conninfo = sjni_dup_bytes(env, jconninfo, NULL)) == NULL)
		return 0;
	conn = sjni_alloc(env, sizeof *conn);
	if (conn == NULL)
	{
		free(conninfo);
		return 0;
	}
	memset(conn, 0, sizeof *conn);
	conn->pg = sjni_pq.connectStart(conninfo);
	free(conninfo);
	if (conn->pg == NULL)
	{
		free(conn);
		sjni_throw(env, SJNI_KIND_OUT_OF_MEMORY,
				   "out of memory: the library returned no connection object");
		return 0;
	}
	sjni_notice_install(conn);
	if (sjni_pq.status(conn->pg) != SJNI_CONNECTION_BAD && !finish_connect(conn, timeout_ms))
	{
		sjni_pq.finish(conn->pg);
		sjni_notice_discard(conn);
		free(conn);
		sjni_throw(env, SJNI_KIND_CALL_FAILED, "timeout expired");
		return 0;
	}
	return (jlong) (intptr_t) conn;
}

JNIEXPORT void JNICALL
Java_org_stuntgres_pq_ConnBase_finish0(JNIEnv *env, jclass cls, jlong handle)
{
	sjni_conn  *conn = (sjni_conn *) (intptr_t) handle;

	(void) env;
	(void) cls;
	if (conn == NULL)
		return;
	sjni_pq.finish(conn->pg);
	sjni_notice_discard(conn);
	free(conn);
}

JNIEXPORT jint JNICALL
Java_org_stuntgres_pq_ConnBase_status0(JNIEnv *env, jclass cls, jlong handle)
{
	sjni_conn  *conn = sjni_conn_of(env, handle);

	(void) cls;
	return conn == NULL ? SJNI_CONNECTION_BAD : sjni_pq.status(conn->pg);
}

JNIEXPORT jbyteArray JNICALL
Java_org_stuntgres_pq_ConnBase_errorMessage0(JNIEnv *env, jclass cls, jlong handle)
{
	sjni_conn  *conn = sjni_conn_of(env, handle);
	const char *message;

	(void) cls;
	if (conn == NULL)
		return NULL;
	message = sjni_pq.errorMessage(conn->pg);
	if (message == NULL)
		message = "";
	return sjni_new_bytes(env, message, strlen(message));
}
```

### 5.3 Session accessors (`sjni_session.c`, PQ-04B)

`transactionStatus0`, `serverVersion0`, `backendPid0`: `sjni_conn_of`, one accessor
call. `parameterStatus0(handle, name)`: copy `name`; `value = parameterStatus(conn,
name)`; free the copy; `NULL` → return null; else a new array with `strlen(value)`
bytes. The complete file:

```c
/* sjni_session.c - facts about the session: transaction status, parameters, ids. */
#include <stdlib.h>
#include <string.h>

#include "sjni.h"
#include "org_stuntgres_pq_ConnSession.h"

JNIEXPORT jint JNICALL
Java_org_stuntgres_pq_ConnSession_transactionStatus0(JNIEnv *env, jclass cls, jlong handle)
{
	sjni_conn  *conn = sjni_conn_of(env, handle);

	(void) cls;
	return conn == NULL ? SJNI_TRANS_UNKNOWN : sjni_pq.transactionStatus(conn->pg);
}

JNIEXPORT jbyteArray JNICALL
Java_org_stuntgres_pq_ConnSession_parameterStatus0(JNIEnv *env, jclass cls, jlong handle,
												jbyteArray jname)
{
	sjni_conn  *conn = sjni_conn_of(env, handle);
	char	   *name;
	const char *value;

	(void) cls;
	if (conn == NULL)
		return NULL;
	if ((name = sjni_dup_bytes(env, jname, NULL)) == NULL)
		return NULL;
	value = sjni_pq.parameterStatus(conn->pg, name);
	free(name);
	if (value == NULL)
		return NULL;
	return sjni_new_bytes(env, value, strlen(value));
}

JNIEXPORT jint JNICALL
Java_org_stuntgres_pq_ConnSession_serverVersion0(JNIEnv *env, jclass cls, jlong handle)
{
	sjni_conn  *conn = sjni_conn_of(env, handle);

	(void) cls;
	return conn == NULL ? 0 : sjni_pq.serverVersion(conn->pg);
}

JNIEXPORT jint JNICALL
Java_org_stuntgres_pq_ConnSession_backendPid0(JNIEnv *env, jclass cls, jlong handle)
{
	sjni_conn  *conn = sjni_conn_of(env, handle);

	(void) cls;
	return conn == NULL ? 0 : sjni_pq.backendPID(conn->pg);
}
```

### 5.4 Simple execution (`sjni_exec.c`, PQ-06A)

`sendQuery0(handle, sql)`: copy `sql`; `ok = sendQuery(conn, sql)`; free; return it as
a boolean. `getResult0(handle)`: `res = getResult(conn)` (blocks until the next result
is complete); `NULL` → return null (the command is complete; this is not an error);
else `sjni_result_to_java(env, conn, res)`. The loop over the results of a statement
string and its stop rule for COPY are in Java (`ConnExec.execAll`, 9.5). `exec0` (the
blocking form that returns the last result) is in `sjni_result.c` (6.2). The complete
file:

```c
/* sjni_exec.c - sending a statement string and collecting each of its results. */
#include <stdlib.h>

#include "sjni.h"
#include "org_stuntgres_pq_ConnExec.h"

JNIEXPORT jboolean JNICALL
Java_org_stuntgres_pq_ConnExec_sendQuery0(JNIEnv *env, jclass cls, jlong handle, jbyteArray jsql)
{
	sjni_conn  *conn = sjni_conn_of(env, handle);
	char	   *sql;
	int			ok;

	(void) cls;
	if (conn == NULL)
		return JNI_FALSE;
	if ((sql = sjni_dup_bytes(env, jsql, NULL)) == NULL)
		return JNI_FALSE;
	ok = sjni_pq.sendQuery(conn->pg, sql);
	free(sql);
	return ok ? JNI_TRUE : JNI_FALSE;
}

JNIEXPORT jobject JNICALL
Java_org_stuntgres_pq_ConnExec_getResult0(JNIEnv *env, jclass cls, jlong handle)
{
	sjni_conn  *conn = sjni_conn_of(env, handle);
	sjni_pgresult *res;

	(void) cls;
	if (conn == NULL)
		return NULL;
	res = sjni_pq.getResult(conn->pg);
	if (res == NULL)
		return NULL;
	return sjni_result_to_java(env, conn, res);
}
```

### 5.5 Parameter arrays (`sjni_params.c`, PQ-06B)

`sjni_params_load(env, types, values, formats, p)`, used by rows 15 and 17:

1. Zero `*p`; `count = length(values)`; with 0 parameters return 1 (all pointers stay
   `NULL`, which the library accepts for `nParams` 0).
2. Allocate `values[count]` and `lengths[count]`.
3. First pass over `values`: `lengths[i]` = length of element `i` (0 for a null
   element); `total` += length + 1 for each non-null element.
4. Allocate `arena[total]`. Second pass: a null element gives `values[i] = NULL` (SQL
   NULL); otherwise copy the element into the arena with `GetByteArrayRegion`, write a
   zero byte after it, point `values[i]` at it. Local references are deleted in the
   loop, so 65535 parameters do not overflow the local frame.
5. When `types` is not null: allocate and copy `count` OIDs. When `formats` is not
   null: allocate and copy `count` formats.
6. Any failed allocation returns 0 with OUT_OF_MEMORY pending; the caller calls
   `sjni_params_free`, which frees whatever was allocated.

`execParams0(handle, sql, types, values, formats)`: copy `sql`; load the parameters;
`res = execParams(conn, sql, count, types, values, lengths, formats, 0)`; free; return
`sjni_result_to_java`. The lengths are always passed: the library ignores them for
text-format values and needs them for binary ones. Results are always requested in text
format (plan B6).

Why formats exist at this level **(measured, experiment 7)**: through pgjdbc a
`setString` value with a NUL character gives the server error SQLSTATE `22021`,
`invalid byte sequence for encoding "UTF8": 0x00`, context `unnamed portal parameter
$1`. A text-format value is a C string and cannot carry the byte; the same value sent in
binary format (type 0, 25 or 1043) gives exactly that error with that context. Whether
the driver does so is decided by JDBC-DESIGN-TYPES; this layer makes it possible.

The complete file:

```c
/* sjni_params.c - parameter arrays and parameterized execution. */
#include <stdlib.h>
#include <string.h>

#include "sjni.h"
#include "org_stuntgres_pq_ConnParams.h"

int
sjni_params_load(JNIEnv *env, jintArray types, jobjectArray values, jintArray formats,
				 sjni_params *p)
{
	size_t		total = 0;
	size_t		used = 0;
	int			count = (*env)->GetArrayLength(env, values);

	memset(p, 0, sizeof *p);
	p->count = count;
	if (count == 0)
		return 1;
	p->values = sjni_alloc(env, (size_t) count * sizeof *p->values);
	if (p->values == NULL)
		return 0;
	p->lengths = sjni_alloc(env, (size_t) count * sizeof *p->lengths);
	if (p->lengths == NULL)
		return 0;
	for (int i = 0; i < count; i++)
	{
		jbyteArray	item = (jbyteArray) (*env)->GetObjectArrayElement(env, values, i);

		p->lengths[i] = 0;
		if (item != NULL)
		{
			p->lengths[i] = (*env)->GetArrayLength(env, item);
			total += (size_t) p->lengths[i] + 1;
			(*env)->DeleteLocalRef(env, item);
		}
	}
	p->arena = sjni_alloc(env, total);
	if (p->arena == NULL)
		return 0;
	for (int i = 0; i < count; i++)
	{
		jbyteArray	item = (jbyteArray) (*env)->GetObjectArrayElement(env, values, i);

		if (item == NULL)
		{
			p->values[i] = NULL;
			continue;
		}
		(*env)->GetByteArrayRegion(env, item, 0, p->lengths[i], (jbyte *) p->arena + used);
		(*env)->DeleteLocalRef(env, item);
		p->arena[used + (size_t) p->lengths[i]] = '\0';
		p->values[i] = p->arena + used;
		used += (size_t) p->lengths[i] + 1;
	}
	if (types != NULL)
	{
		p->types = sjni_alloc(env, (size_t) count * sizeof *p->types);
		if (p->types == NULL)
			return 0;
		(*env)->GetIntArrayRegion(env, types, 0, count, (jint *) p->types);
	}
	if (formats != NULL)
	{
		p->formats = sjni_alloc(env, (size_t) count * sizeof *p->formats);
		if (p->formats == NULL)
			return 0;
		(*env)->GetIntArrayRegion(env, formats, 0, count, (jint *) p->formats);
	}
	return 1;
}

void
sjni_params_free(sjni_params *p)
{
	free(p->types);
	free((void *) p->values);
	free(p->lengths);
	free(p->formats);
	free(p->arena);
	memset(p, 0, sizeof *p);
}

JNIEXPORT jobject JNICALL
Java_org_stuntgres_pq_ConnParams_execParams0(JNIEnv *env, jclass cls, jlong handle,
										   jbyteArray jsql, jintArray jtypes,
										   jobjectArray jvalues, jintArray jformats)
{
	sjni_conn  *conn = sjni_conn_of(env, handle);
	sjni_params params;
	sjni_pgresult *res;
	char	   *sql;

	(void) cls;
	if (conn == NULL)
		return NULL;
	if ((sql = sjni_dup_bytes(env, jsql, NULL)) == NULL)
		return NULL;
	if (!sjni_params_load(env, jtypes, jvalues, jformats, &params))
	{
		sjni_params_free(&params);
		free(sql);
		return NULL;
	}
	res = sjni_pq.execParams(conn->pg, sql, params.count, params.types, params.values,
							 params.lengths, params.formats, 0);
	sjni_params_free(&params);
	free(sql);
	return sjni_result_to_java(env, conn, res);
}
```

### 5.6 Prepared statements (`sjni_prepared.c`, PQ-07)

All four functions copy their text arguments, call one library function and return
`sjni_result_to_java`. `prepare0`: `paramTypes` null or empty means 0 declared types.
`execPrepared0`: parameters as in 5.5 without types. `closePrepared0`: when the optional
function is absent raise CALL_FAILED `the loaded library has no closePrepared` (not
reached through `ConnPrepared.closePrepared`, which checks the feature). The complete
file:

```c
/* sjni_prepared.c - prepare, execute prepared, describe, close. */
#include <stdlib.h>

#include "sjni.h"
#include "org_stuntgres_pq_ConnPrepared.h"

JNIEXPORT jobject JNICALL
Java_org_stuntgres_pq_ConnPrepared_prepare0(JNIEnv *env, jclass cls, jlong handle,
											jbyteArray jname, jbyteArray jsql, jintArray jtypes)
{
	sjni_conn  *conn = sjni_conn_of(env, handle);
	char	   *name = NULL;
	char	   *sql = NULL;
	sjni_oid   *types = NULL;
	int			count = 0;
	sjni_pgresult *res;

	(void) cls;
	if (conn == NULL)
		return NULL;
	if ((name = sjni_dup_bytes(env, jname, NULL)) == NULL)
		return NULL;
	if ((sql = sjni_dup_bytes(env, jsql, NULL)) == NULL)
		goto fail;
	if (jtypes != NULL && (count = (*env)->GetArrayLength(env, jtypes)) > 0)
	{
		if ((types = sjni_alloc(env, (size_t) count * sizeof *types)) == NULL)
			goto fail;
		(*env)->GetIntArrayRegion(env, jtypes, 0, count, (jint *) types);
	}
	res = sjni_pq.prepare(conn->pg, name, sql, count, types);
	free(name);
	free(sql);
	free(types);
	return sjni_result_to_java(env, conn, res);

fail:
	free(name);
	free(sql);
	return NULL;
}

JNIEXPORT jobject JNICALL
Java_org_stuntgres_pq_ConnPrepared_execPrepared0(JNIEnv *env, jclass cls, jlong handle,
												 jbyteArray jname, jobjectArray jvalues,
												 jintArray jformats)
{
	sjni_conn  *conn = sjni_conn_of(env, handle);
	sjni_params params;
	sjni_pgresult *res;
	char	   *name;

	(void) cls;
	if (conn == NULL)
		return NULL;
	if ((name = sjni_dup_bytes(env, jname, NULL)) == NULL)
		return NULL;
	if (!sjni_params_load(env, NULL, jvalues, jformats, &params))
	{
		sjni_params_free(&params);
		free(name);
		return NULL;
	}
	res = sjni_pq.execPrepared(conn->pg, name, params.count, params.values, params.lengths,
							   params.formats, 0);
	sjni_params_free(&params);
	free(name);
	return sjni_result_to_java(env, conn, res);
}

JNIEXPORT jobject JNICALL
Java_org_stuntgres_pq_ConnPrepared_describePrepared0(JNIEnv *env, jclass cls, jlong handle,
													 jbyteArray jname)
{
	sjni_conn  *conn = sjni_conn_of(env, handle);
	sjni_pgresult *res;
	char	   *name;

	(void) cls;
	if (conn == NULL)
		return NULL;
	if ((name = sjni_dup_bytes(env, jname, NULL)) == NULL)
		return NULL;
	res = sjni_pq.describePrepared(conn->pg, name);
	free(name);
	return sjni_result_to_java(env, conn, res);
}

JNIEXPORT jobject JNICALL
Java_org_stuntgres_pq_ConnPrepared_closePrepared0(JNIEnv *env, jclass cls, jlong handle,
												  jbyteArray jname)
{
	sjni_conn  *conn = sjni_conn_of(env, handle);
	sjni_pgresult *res;
	char	   *name;

	(void) cls;
	if (conn == NULL)
		return NULL;
	if (sjni_pq.closePrepared == NULL)
	{
		sjni_throw(env, SJNI_KIND_CALL_FAILED, "the loaded library has no closePrepared");
		return NULL;
	}
	if ((name = sjni_dup_bytes(env, jname, NULL)) == NULL)
		return NULL;
	res = sjni_pq.closePrepared(conn->pg, name);
	free(name);
	return sjni_result_to_java(env, conn, res);
}
```

### 5.7 Escaping and client encoding (`sjni_escape.c`, PQ-12)

`escapeLiteral0` / `escapeIdentifier0(handle, text)`: copy `text` with its length;
`escaped = escapeLiteral(conn, text, length)` (or `escapeIdentifier`); free the copy;
`NULL` → CALL_FAILED `escaping a literal failed: <message>` (or `an identifier`); else
copy `strlen(escaped)` bytes to a new array and `freemem(escaped)`.
`setClientEncoding0(handle, name)`: copy, call, free, return the library's value. The
complete file:

```c
/* sjni_escape.c - literal and identifier escaping, client encoding. */
#include <stdlib.h>
#include <string.h>

#include "sjni.h"
#include "org_stuntgres_pq_ConnEscape.h"

static jbyteArray
escape(JNIEnv *env, jlong handle, jbyteArray jtext, int identifier)
{
	sjni_conn  *conn = sjni_conn_of(env, handle);
	jbyteArray	out;
	char	   *text;
	char	   *escaped;
	jsize		len;

	if (conn == NULL)
		return NULL;
	if ((text = sjni_dup_bytes(env, jtext, &len)) == NULL)
		return NULL;
	escaped = identifier
		? sjni_pq.escapeIdentifier(conn->pg, text, (size_t) len)
		: sjni_pq.escapeLiteral(conn->pg, text, (size_t) len);
	free(text);
	if (escaped == NULL)
	{
		sjni_throw_conn(env, conn, identifier ? "escaping an identifier" : "escaping a literal");
		return NULL;
	}
	out = sjni_new_bytes(env, escaped, strlen(escaped));
	sjni_pq.freemem(escaped);
	return out;
}

JNIEXPORT jbyteArray JNICALL
Java_org_stuntgres_pq_ConnEscape_escapeLiteral0(JNIEnv *env, jclass cls, jlong handle,
												jbyteArray jtext)
{
	(void) cls;
	return escape(env, handle, jtext, 0);
}

JNIEXPORT jbyteArray JNICALL
Java_org_stuntgres_pq_ConnEscape_escapeIdentifier0(JNIEnv *env, jclass cls, jlong handle,
												   jbyteArray jtext)
{
	(void) cls;
	return escape(env, handle, jtext, 1);
}

JNIEXPORT jint JNICALL
Java_org_stuntgres_pq_ConnEscape_setClientEncoding0(JNIEnv *env, jclass cls, jlong handle,
													jbyteArray jname)
{
	sjni_conn  *conn = sjni_conn_of(env, handle);
	char	   *name;
	int			rc;

	(void) cls;
	if (conn == NULL)
		return -1;
	if ((name = sjni_dup_bytes(env, jname, NULL)) == NULL)
		return -1;
	rc = sjni_pq.setClientEncoding(conn->pg, name);
	free(name);
	return rc;
}
```

### 5.8 Extensions (`sjni_cluster.c`, PQ-14A; `sjni_ext.c`, PQ-14B)

`version0()`: `sjni_require_loaded`; a new array with the bytes of `Xversion()`.
`clusterDrop0(host, port)`: copy both; `errbuf[1024]`; `ok = XclusterDrop(host, port,
errbuf, 1024)`; free; return null when `ok`, else the bytes of `errbuf`. The complete
file `sjni_cluster.c` (ran against the stand-in library of 10.9, not against libspq,
which does not exist yet):

```c
/* sjni_cluster.c - the SPQX extensions without a connection: version, cluster drop. */
#include <stdlib.h>
#include <string.h>

#include "sjni.h"
#include "org_stuntgres_pq_PqCluster.h"

JNIEXPORT jbyteArray JNICALL
Java_org_stuntgres_pq_PqCluster_version0(JNIEnv *env, jclass cls)
{
	const char *version;

	(void) cls;
	if (!sjni_require_loaded(env))
		return NULL;
	version = sjni_pq.Xversion();
	return sjni_new_bytes(env, version, strlen(version));
}

JNIEXPORT jbyteArray JNICALL
Java_org_stuntgres_pq_PqCluster_clusterDrop0(JNIEnv *env, jclass cls, jbyteArray jhost,
										 jbyteArray jport)
{
	char		errbuf[1024];
	char	   *host;
	char	   *port;
	int			ok;

	(void) cls;
	if (!sjni_require_loaded(env))
		return NULL;
	if ((host = sjni_dup_bytes(env, jhost, NULL)) == NULL)
		return NULL;
	if ((port = sjni_dup_bytes(env, jport, NULL)) == NULL)
	{
		free(host);
		return NULL;
	}
	errbuf[0] = '\0';
	ok = sjni_pq.XclusterDrop(host, port, errbuf, (int) sizeof errbuf);
	free(host);
	free(port);
	if (ok)
		return NULL;
	return sjni_new_bytes(env, errbuf, strlen(errbuf));
}
```

`reset0(handle)`: `Xreset(conn)` as a boolean. `dump0(handle)`: `Xdump(conn, &buffer,
&length)`; 0 → return null; else a new array of `length` bytes and `freemem(buffer)`.
`restore0(handle, dump)`: copy the array with its length; `Xrestore(conn, copy,
length)`; free. The complete file `sjni_ext.c` (**not run**: it compiles and links in
the scratch shim; its functions need libspq):

```c
/* sjni_ext.c - the SPQX extensions on a connection: reset, dump, restore. */
#include <stdlib.h>

#include "sjni.h"
#include "org_stuntgres_pq_PqExt.h"

JNIEXPORT jboolean JNICALL
Java_org_stuntgres_pq_PqExt_reset0(JNIEnv *env, jclass cls, jlong handle)
{
	sjni_conn  *conn = sjni_conn_of(env, handle);

	(void) cls;
	if (conn == NULL)
		return JNI_FALSE;
	return sjni_pq.Xreset(conn->pg) ? JNI_TRUE : JNI_FALSE;
}

JNIEXPORT jbyteArray JNICALL
Java_org_stuntgres_pq_PqExt_dump0(JNIEnv *env, jclass cls, jlong handle)
{
	sjni_conn  *conn = sjni_conn_of(env, handle);
	char	   *buffer = NULL;
	size_t		length = 0;
	jbyteArray	out;

	(void) cls;
	if (conn == NULL)
		return NULL;
	if (!sjni_pq.Xdump(conn->pg, &buffer, &length))
		return NULL;
	out = sjni_new_bytes(env, buffer, length);
	sjni_pq.freemem(buffer);
	return out;
}

JNIEXPORT jboolean JNICALL
Java_org_stuntgres_pq_PqExt_restore0(JNIEnv *env, jclass cls, jlong handle, jbyteArray jdump)
{
	sjni_conn  *conn = sjni_conn_of(env, handle);
	char	   *dump;
	jsize		length;
	int			ok;

	(void) cls;
	if (conn == NULL)
		return JNI_FALSE;
	if ((dump = sjni_dup_bytes(env, jdump, &length)) == NULL)
		return JNI_FALSE;
	ok = sjni_pq.Xrestore(conn->pg, dump, (size_t) length);
	free(dump);
	return ok ? JNI_TRUE : JNI_FALSE;
}
```

### 5.9 Functions of docs/LIBSPQ.md section 3 that the shim does not bind

docs/LIBSPQ.md section 3 lists 104 functions of the libpq subset and 5 extensions. The
shim binds 62 of them (4.3). The other 47:

| Functions | Reason |
|---|---|
| `SPQconnectdb`, `SPQconnectdbParams`, `SPQconnectStartParams` | One way to connect: `SPQconnectStart` with a conninfo string, so that the notice receiver is in place before the session starts (7.1). The driver builds the string with `Conninfo` (9.3). |
| `SPQreset` | The driver opens a new connection instead; a JDBC connection is never revived. |
| `SPQping`, `SPQpingParams` | JDBC has no ping without a connection; `Connection.isValid` runs a statement. |
| `SPQconninfoParse`, `SPQconndefaults`, `SPQconninfo`, `SPQconninfoFree` | The driver parses JDBC URLs, not conninfo strings, and passes every value explicitly. |
| `SPQdb`, `SPQuser`, `SPQpass`, `SPQhost`, `SPQport`, `SPQoptions` | The driver keeps the values it passed. |
| `SPQprotocolVersion` | Always 3. |
| `SPQclientEncoding`, `spg_encoding_to_char`, `spg_char_to_encoding` | The encoding name comes from `parameterStatus("client_encoding")`. No `spg_` function is bound, so the shim needs one prefix only. |
| `SPQisthreadsafe` | Always 1. |
| `SPQsendQueryParams`, `SPQsendPrepare`, `SPQsendQueryPrepared`, `SPQsendDescribePrepared`, `SPQsendDescribePortal`, `SPQsendClosePrepared`, `SPQsendClosePortal` | The blocking forms are used; only a statement string can yield several results, and that is `SPQsendQuery`. |
| `SPQisBusy`, `SPQflush`, `SPQsetnonblocking`, `SPQisnonblocking` | Blocking use only (plan B3). |
| `SPQsetSingleRowMode` | Results are fetched completely (plan J11). |
| `SPQdescribePortal` | The driver uses no named portal. |
| `SPQresStatus` | Java has the names (`ExecStatus.name`). |
| `SPQbinaryTuples` | The per-column format (`fformat`) is copied instead. |
| `SPQfnumber` | Column lookup by name is done in Java, with JDBC's own matching rules. |
| `SPQresultMemorySize` | Not needed: the result is copied and cleared in one call. |
| `SPQresultVerboseErrorMessage`, `SPQsetErrorVerbosity`, `SPQsetErrorContextVisibility` | The driver builds its messages from the error fields, which are always copied in full. |
| `SPQsetNoticeProcessor` | The receiver (all fields) is used, not the processor (text only). |
| `SPQescapeStringConn`, `SPQescapeString`, `SPQescapeByteaConn`, `SPQescapeBytea`, `SPQunescapeBytea` | `escapeLiteral` and `escapeIdentifier` cover text; bytea text is produced and parsed by the Java codecs (JDBC-DESIGN-TYPES). |

The planned extensions of CABI 3.2 (`SPQXsendBindPortal`, `SPQXsendExecutePortal`,
`SPQXbindPortal`, `SPQXexecutePortal`, `SPQXresultSuspended`, `SPQXsetNoticeQueue`,
`SPQXnextNotice`) are not part of docs/LIBSPQ.md section 3 and are not bound.


## 6. Result copy

One native call turns one library result into one `PqResult` and clears the library
result before it returns. A `PqResult` holds no native memory; it is immutable and may
be read by any thread, kept as long as wanted and never needs closing.

### 6.1 The result object

`org.stuntgres.pq.PqResult`, field by field. The constructor is package-private and is
called from C with the JNI signature `(I[B[BIII[B[I[B[I[I[B[B)V`; the arguments arrive
in the order of this table.

| # | Field | Type | Content |
|---|---|---|---|
| 1 | `status` | `int` | `resultStatus`: a value of `ExecStatus` (9.2). |
| 2 | `commandStatus` | `byte[]` | `cmdStatus`: the command tag, for example `INSERT 0 2`, `SELECT 2`, `CREATE TABLE`; empty when the result has none (errors, a describe or prepare result, `COPY` start). |
| 3 | `commandTuples` | `byte[]` | `cmdTuples`: the affected-row count as decimal text (`2`), empty for commands without one. |
| 4 | `oidValue` | `int` | `oidValue`: OID bits, 0 when none. The accessor returns it unsigned as `long`. |
| 5 | `columnCount` | `int` | `nfields`. |
| 6 | `rowCount` | `int` | `ntuples`. |
| 7 | `fieldNames` | `byte[]` | The column names, concatenated without separators. |
| 8 | `fieldMeta` | `int[]` | Seven ints per column `c` at index `7 * c`: `[0]` end offset of the name in `fieldNames` (the name starts where the previous one ends, at 0 for the first), `[1]` table OID (`ftable`), `[2]` attribute number in the table (`ftablecol`), `[3]` type OID (`ftype`), `[4]` type size (`fsize`), `[5]` type modifier (`fmod`), `[6]` format (`fformat`: 0 text, 1 binary). |
| 9 | `data` | `byte[]` | Every non-NULL value, row by row, column by column, without separators or terminators. |
| 10 | `ends` | `int[]` | One int per value, index `row * columnCount + column`: the offset in `data` at which the value ends; for SQL NULL the bitwise complement (`~end`) of the offset the value would end at, which is negative. A value starts where the previous one ends (decode a negative entry with `~`), the first at 0. Length = end − start; NULL has length 0. |
| 11 | `paramTypes` | `int[]` | `paramtype` for each of `nparams` parameters: filled for a describe result, otherwise empty. |
| 12 | `errorFields` | `byte[]` | The error or notice fields, packed: for each present field its code byte (9.2), its value, one zero byte, in the order `S V C M D H P p q W s t c d n F L R`. Empty when the result is not an error. |
| 13 | `errorMessage` | `byte[]` | `resultErrorMessage`: the message as the library formats it, with its trailing line feed; empty when there is none. |

No array is ever null. Accessors (complete signatures in the file below): `status()`,
`commandStatus()`, `commandTuples()`, `oidValue()`, `columnCount()`, `rowCount()`,
`fieldName(int)`, `fieldIndex(String)`, `tableOid(int)`, `tableColumn(int)`,
`typeOid(int)`, `typeSize(int)`, `typeModifier(int)`, `format(int)`, `isNull(int, int)`,
`offset(int, int)`, `length(int, int)`, `data()`, `bytes(int, int)`, `string(int, int)`,
`parameterCount()`, `parameterType(int)`, `errorField(int)`, `sqlState()`,
`errorMessage()`. A row or column out of range raises `IndexOutOfBoundsException`
(`Objects.checkIndex`). `data()` returns the array itself for callers that parse values
in place (`offset` and `length` say where); they must not modify it. Text accessors
decode UTF-8; a malformed sequence becomes U+FFFD (9.3).

Things to know about library results **(measured)**:

- The columns of a `COPY_IN` or `COPY_OUT` result have no name (the library returns a
  null pointer); the copy stores an empty name, type 0, size 0, modifier 0.
- A connection-level failure arrives as a FATAL_ERROR result without any field:
  `sqlState()` is null and `errorMessage()` carries the text.
- An empty statement string gives one result with status EMPTY_QUERY.
- `describePrepared` gives COMMAND_OK with columns, parameter types and no rows.
- The stuntgres server does not send the fields `F`, `L` and `R` (source file, line and
  function of the PostgreSQL server); no test asserts them.

The complete file `PqResult.java` (PQ-05A):

```java
package org.stuntgres.pq;

import java.util.Objects;

/**
 * One result of a command, copied out of native memory: status, command tag, field
 * descriptions, all values, parameter types of a describe result and error fields.
 * Immutable; any thread may read it.
 */
public final class PqResult {
    private static final int META = 7;

    private final int status;
    private final byte[] commandStatus;
    private final byte[] commandTuples;
    private final int oidValue;
    private final int columnCount;
    private final int rowCount;
    private final byte[] fieldNames;
    private final int[] fieldMeta;
    private final byte[] data;
    private final int[] ends;
    private final int[] paramTypes;
    private final byte[] errorFields;
    private final byte[] errorMessage;

    /**
     * Called by the shim (sjni_result.c) and by tests; the arrays are owned by the new
     * object and never modified afterwards.
     */
    PqResult(int status, byte[] commandStatus, byte[] commandTuples, int oidValue,
            int columnCount, int rowCount, byte[] fieldNames, int[] fieldMeta, byte[] data,
            int[] ends, int[] paramTypes, byte[] errorFields, byte[] errorMessage) {
        this.status = status;
        this.commandStatus = commandStatus;
        this.commandTuples = commandTuples;
        this.oidValue = oidValue;
        this.columnCount = columnCount;
        this.rowCount = rowCount;
        this.fieldNames = fieldNames;
        this.fieldMeta = fieldMeta;
        this.data = data;
        this.ends = ends;
        this.paramTypes = paramTypes;
        this.errorFields = errorFields;
        this.errorMessage = errorMessage;
    }

    /**
     * Returns the result status.
     *
     * @return a constant of {@link ExecStatus}
     */
    public int status() {
        return status;
    }

    /**
     * Returns the command tag.
     *
     * @return for example {@code "INSERT 0 1"}; empty when there is none
     */
    public String commandStatus() {
        return PqText.decode(commandStatus, 0, commandStatus.length);
    }

    /**
     * Returns the number of rows the command affected, as the library reports it.
     *
     * @return decimal digits, or an empty string for commands without a row count
     */
    public String commandTuples() {
        return PqText.decode(commandTuples, 0, commandTuples.length);
    }

    /**
     * Returns the OID of the inserted row.
     *
     * @return the OID as an unsigned value; 0 when there is none
     */
    public long oidValue() {
        return oidValue & 0xFFFFFFFFL;
    }

    /**
     * Returns the number of columns.
     *
     * @return the number of fields
     */
    public int columnCount() {
        return columnCount;
    }

    /**
     * Returns the number of rows.
     *
     * @return the number of tuples
     */
    public int rowCount() {
        return rowCount;
    }

    private int meta(int column, int item) {
        Objects.checkIndex(column, columnCount);
        return fieldMeta[column * META + item];
    }

    /**
     * Returns a column name.
     *
     * @param column the column number, from 0
     * @return the name
     */
    public String fieldName(int column) {
        int end = meta(column, 0);
        int start = column == 0 ? 0 : fieldMeta[(column - 1) * META];
        return PqText.decode(fieldNames, start, end - start);
    }

    /**
     * Finds the first column with a name; the comparison is exact.
     *
     * @param name the column name
     * @return the column number, or -1
     */
    public int fieldIndex(String name) {
        for (int column = 0; column < columnCount; column++) {
            if (fieldName(column).equals(name)) {
                return column;
            }
        }
        return -1;
    }

    /**
     * Returns the OID of the table a column comes from.
     *
     * @param column the column number, from 0
     * @return the OID bits; 0 when the column is not a table column
     */
    public int tableOid(int column) {
        return meta(column, 1);
    }

    /**
     * Returns the attribute number of a column within its table.
     *
     * @param column the column number, from 0
     * @return the number from 1; 0 when the column is not a table column
     */
    public int tableColumn(int column) {
        return meta(column, 2);
    }

    /**
     * Returns the type OID of a column.
     *
     * @param column the column number, from 0
     * @return the OID bits
     */
    public int typeOid(int column) {
        return meta(column, 3);
    }

    /**
     * Returns the type size of a column.
     *
     * @param column the column number, from 0
     * @return the size in bytes; negative for a variable-length type
     */
    public int typeSize(int column) {
        return meta(column, 4);
    }

    /**
     * Returns the type modifier of a column.
     *
     * @param column the column number, from 0
     * @return the modifier; -1 when there is none
     */
    public int typeModifier(int column) {
        return meta(column, 5);
    }

    /**
     * Returns the format of a column.
     *
     * @param column the column number, from 0
     * @return 0 for text, 1 for binary
     */
    public int format(int column) {
        return meta(column, 6);
    }

    private int cell(int row, int column) {
        Objects.checkIndex(row, rowCount);
        Objects.checkIndex(column, columnCount);
        return row * columnCount + column;
    }

    private static int end(int mark) {
        return mark < 0 ? ~mark : mark;
    }

    /**
     * Tells whether a value is SQL NULL.
     *
     * @param row the row number, from 0
     * @param column the column number, from 0
     * @return true for NULL
     */
    public boolean isNull(int row, int column) {
        return ends[cell(row, column)] < 0;
    }

    /**
     * Returns the position of a value inside {@link #data()}.
     *
     * @param row the row number, from 0
     * @param column the column number, from 0
     * @return the offset of its first byte
     */
    public int offset(int row, int column) {
        int cell = cell(row, column);
        return cell == 0 ? 0 : end(ends[cell - 1]);
    }

    /**
     * Returns the length of a value in bytes.
     *
     * @param row the row number, from 0
     * @param column the column number, from 0
     * @return the length; 0 for NULL
     */
    public int length(int row, int column) {
        int cell = cell(row, column);
        return end(ends[cell]) - (cell == 0 ? 0 : end(ends[cell - 1]));
    }

    /**
     * Returns the buffer that holds every value. Callers must not modify it.
     *
     * @return the buffer
     */
    public byte[] data() {
        return data;
    }

    /**
     * Returns a copy of a value.
     *
     * @param row the row number, from 0
     * @param column the column number, from 0
     * @return the bytes, or null for NULL
     */
    public byte[] bytes(int row, int column) {
        if (isNull(row, column)) {
            return null;
        }
        int start = offset(row, column);
        byte[] copy = new byte[length(row, column)];
        System.arraycopy(data, start, copy, 0, copy.length);
        return copy;
    }

    /**
     * Returns a value decoded as UTF-8.
     *
     * @param row the row number, from 0
     * @param column the column number, from 0
     * @return the text, or null for NULL
     */
    public String string(int row, int column) {
        if (isNull(row, column)) {
            return null;
        }
        return PqText.decode(data, offset(row, column), length(row, column));
    }

    /**
     * Returns the number of parameters of a described statement.
     *
     * @return the count; 0 for other results
     */
    public int parameterCount() {
        return paramTypes.length;
    }

    /**
     * Returns the type of a parameter of a described statement.
     *
     * @param index the parameter number, from 0
     * @return the OID bits
     */
    public int parameterType(int index) {
        return paramTypes[index];
    }

    /**
     * Returns a field of the error or notice this result carries.
     *
     * @param code a constant of {@link DiagField}
     * @return the value, or null when the field is absent
     */
    public String errorField(int code) {
        return PqText.field(errorFields, code);
    }

    /**
     * Returns the SQLSTATE of an error result.
     *
     * @return the five-character code, or null when the result is not an error
     */
    public String sqlState() {
        return errorField(DiagField.SQLSTATE);
    }

    /**
     * Returns the error message as the library formats it.
     *
     * @return the message without trailing line feeds; empty when there is no error
     */
    public String errorMessage() {
        return PqText.trimNewlines(PqText.decode(errorMessage, 0, errorMessage.length));
    }
}
```

### 6.2 The copy (`sjni_result.c`, PQ-05B)

`jobject sjni_result_to_java(JNIEnv *env, sjni_conn *conn, sjni_pgresult *res)`:

1. `res` is `NULL`: raise CALL_FAILED `the command failed: <errorMessage of the
   connection>` and return `NULL`.
2. `result = build(env, res)`; `clear(res)`; return `result`. `clear` runs whether
   `build` succeeded or left an exception pending.

`build`:

1. `rows = ntuples`, `cols = nfields`, `nparams = nparams`. If `rows * cols` (64-bit)
   exceeds `SJNI_MAX_ARRAY` (`0x7FFFFFF7`), raise TOO_LARGE `the result has <rows> rows
   of <cols> columns: too many values for a Java array`.
2. First pass over all cells: `total` (64-bit) = sum of `getlength` of the cells that
   are not NULL (`getisnull`). If it exceeds `SJNI_MAX_ARRAY`, raise TOO_LARGE `the
   result holds <total> bytes of values: too large for a Java array`.
3. `EnsureLocalCapacity(16)`. Make `commandStatus` and `commandTuples` (a null pointer
   from the library counts as an empty string everywhere in this file).
4. Fields (`copy_fields`): sum the name lengths; allocate one C buffer for the names
   and one for `7 * cols` ints; fill both in one loop; make `fieldNames` and `fieldMeta`
   with one `NewByteArray`/`NewIntArray` and one region copy each; free the C buffers.
5. `data = NewByteArray(total)`, `ends = NewIntArray(rows * cols)`.
6. Values (`copy_cells`): allocate a staging buffer of 65536 bytes and a C index of
   `rows * cols` ints. For each cell in row-major order: NULL → index entry
   `~(bytes so far)`; otherwise, if the value does not fit in the free part of the
   staging buffer, flush the buffer to `data` with `SetByteArrayRegion`; a value longer
   than the whole buffer is then written to `data` directly from the library's memory
   with one `SetByteArrayRegion`; any other value is appended to the staging buffer with
   `memcpy`; index entry = bytes so far including this value. At the end flush the
   buffer and write the index with one `SetIntArrayRegion`. So the number of JNI calls
   is about `total / 65536` plus one per value above 64 KiB plus one for the index, and
   no C memory proportional to `total` is needed.
7. `paramTypes = NewIntArray(nparams)`, one `SetIntArrayRegion` per parameter.
8. Error fields: `sjni_pack_fields(res, NULL)` gives the size; allocate, pack, make the
   array, free. `errorMessage` from `resultErrorMessage`.
9. `FindClass("org/stuntgres/pq/PqResult")`, `GetMethodID("<init>", ...)`, `NewObject`
   with the thirteen arguments.
10. Any step that fails (an array could not be made: `OutOfMemoryError` pending; a C
    allocation failed: OUT_OF_MEMORY) returns `NULL` at once; local references are
    released by the JVM when the native method returns.

`ResultCopy.exec0(handle, sql)` (the entry the tests of this part use, and
`ConnExec.exec` later): copy `sql`; `res = exec(conn, sql)`; free; return
`sjni_result_to_java`.

The complete files `sjni_result.c` and `ResultCopy.java`:

```c
/* sjni_result.c - one library result becomes one org.stuntgres.pq.PqResult. */
#include <stdlib.h>
#include <string.h>

#include "sjni.h"
#include "org_stuntgres_pq_ResultCopy.h"

#define STAGE_BYTES 65536		/* staging buffer of the value copy */
#define META_INTS 7				/* ints per field in fieldMeta */

/* Library strings may be NULL (the column names of a COPY result are). */
static const char *
text(const char *s)
{
	return s ? s : "";
}

/* Copy every value into data and every cell's end offset into ends. */
static int
copy_cells(JNIEnv *env, const sjni_pgresult *res, int rows, int cols,
		   jbyteArray data, jintArray ends)
{
	char	   *stage = sjni_alloc(env, STAGE_BYTES);
	jint	   *index;
	size_t		ncells = (size_t) rows * (size_t) cols;
	size_t		cell = 0;
	jsize		flushed = 0;	/* bytes already in data */
	jsize		staged = 0;		/* bytes waiting in stage */

	if (stage == NULL)
		return 0;
	index = sjni_alloc(env, (ncells ? ncells : 1) * sizeof *index);
	if (index == NULL)
	{
		free(stage);
		return 0;
	}
	for (int r = 0; r < rows; r++)
	{
		for (int c = 0; c < cols; c++, cell++)
		{
			int			len;

			if (sjni_pq.getisnull(res, r, c))
			{
				index[cell] = ~(flushed + staged);
				continue;
			}
			len = sjni_pq.getlength(res, r, c);
			if (len > STAGE_BYTES - staged)
			{
				if (staged > 0)
				{
					(*env)->SetByteArrayRegion(env, data, flushed, staged, (const jbyte *) stage);
					flushed += staged;
					staged = 0;
				}
				if (len > STAGE_BYTES)
				{
					(*env)->SetByteArrayRegion(env, data, flushed, len,
											   (const jbyte *) sjni_pq.getvalue(res, r, c));
					flushed += len;
					len = 0;
				}
			}
			if (len > 0)
			{
				memcpy(stage + staged, sjni_pq.getvalue(res, r, c), (size_t) len);
				staged += len;
			}
			index[cell] = flushed + staged;
		}
	}
	if (staged > 0)
		(*env)->SetByteArrayRegion(env, data, flushed, staged, (const jbyte *) stage);
	if (ncells > 0)
		(*env)->SetIntArrayRegion(env, ends, 0, (jsize) ncells, index);
	free(stage);
	free(index);
	return 1;
}

/* Build the field arrays: names concatenated, META_INTS ints per field. */
static int
copy_fields(JNIEnv *env, const sjni_pgresult *res, int cols,
			jbyteArray *names_out, jintArray *meta_out)
{
	size_t		total = 0;
	char	   *names;
	jint	   *meta;
	size_t		used = 0;

	for (int c = 0; c < cols; c++)
		total += strlen(text(sjni_pq.fname(res, c)));
	names = sjni_alloc(env, total);
	if (names == NULL)
		return 0;
	meta = sjni_alloc(env, (size_t) (cols ? cols : 1) * META_INTS * sizeof *meta);
	if (meta == NULL)
	{
		free(names);
		return 0;
	}
	for (int c = 0; c < cols; c++)
	{
		const char *name = text(sjni_pq.fname(res, c));
		size_t		n = strlen(name);
		jint	   *m = meta + (size_t) c * META_INTS;

		memcpy(names + used, name, n);
		used += n;
		m[0] = (jint) used;
		m[1] = (jint) sjni_pq.ftable(res, c);
		m[2] = sjni_pq.ftablecol(res, c);
		m[3] = (jint) sjni_pq.ftype(res, c);
		m[4] = sjni_pq.fsize(res, c);
		m[5] = sjni_pq.fmod(res, c);
		m[6] = sjni_pq.fformat(res, c);
	}
	*names_out = sjni_new_bytes(env, names, total);
	if (*names_out != NULL)
	{
		*meta_out = (*env)->NewIntArray(env, cols * META_INTS);
		if (*meta_out != NULL && cols > 0)
			(*env)->SetIntArrayRegion(env, *meta_out, 0, cols * META_INTS, meta);
	}
	free(names);
	free(meta);
	return *names_out != NULL && *meta_out != NULL;
}

static jobject
build(JNIEnv *env, sjni_pgresult *res)
{
	int			rows = sjni_pq.ntuples(res);
	int			cols = sjni_pq.nfields(res);
	int			nparams = sjni_pq.nparams(res);
	uint64_t	ncells = (uint64_t) rows * (uint64_t) cols;
	uint64_t	total = 0;
	const char *str;
	char	   *packed;
	size_t		packed_len;
	jclass		cls;
	jmethodID	ctor;
	jbyteArray	command_status,
				command_tuples,
				names = NULL,
				data,
				error_fields,
				error_message;
	jintArray	meta = NULL,
				ends,
				param_types;

	if (ncells > SJNI_MAX_ARRAY)
	{
		sjni_throw(env, SJNI_KIND_TOO_LARGE,
				   "the result has %d rows of %d columns: too many values for a Java array",
				   rows, cols);
		return NULL;
	}
	for (int r = 0; r < rows; r++)
		for (int c = 0; c < cols; c++)
			if (!sjni_pq.getisnull(res, r, c))
				total += (uint64_t) sjni_pq.getlength(res, r, c);
	if (total > SJNI_MAX_ARRAY)
	{
		sjni_throw(env, SJNI_KIND_TOO_LARGE,
				   "the result holds %llu bytes of values: too large for a Java array",
				   (unsigned long long) total);
		return NULL;
	}

	if ((*env)->EnsureLocalCapacity(env, 16) != 0)
		return NULL;
	str = text(sjni_pq.cmdStatus(res));
	if ((command_status = sjni_new_bytes(env, str, strlen(str))) == NULL)
		return NULL;
	str = text(sjni_pq.cmdTuples(res));
	if ((command_tuples = sjni_new_bytes(env, str, strlen(str))) == NULL)
		return NULL;
	if (!copy_fields(env, res, cols, &names, &meta))
		return NULL;
	if ((data = (*env)->NewByteArray(env, (jsize) total)) == NULL)
		return NULL;
	if ((ends = (*env)->NewIntArray(env, (jsize) ncells)) == NULL)
		return NULL;
	if (!copy_cells(env, res, rows, cols, data, ends))
		return NULL;

	if ((param_types = (*env)->NewIntArray(env, nparams)) == NULL)
		return NULL;
	for (int i = 0; i < nparams; i++)
	{
		jint		oid = (jint) sjni_pq.paramtype(res, i);

		(*env)->SetIntArrayRegion(env, param_types, i, 1, &oid);
	}

	packed_len = sjni_pack_fields(res, NULL);
	if ((packed = sjni_alloc(env, packed_len)) == NULL)
		return NULL;
	sjni_pack_fields(res, packed);
	error_fields = sjni_new_bytes(env, packed, packed_len);
	free(packed);
	if (error_fields == NULL)
		return NULL;
	str = text(sjni_pq.resultErrorMessage(res));
	if ((error_message = sjni_new_bytes(env, str, strlen(str))) == NULL)
		return NULL;

	if ((cls = (*env)->FindClass(env, "org/stuntgres/pq/PqResult")) == NULL)
		return NULL;
	ctor = (*env)->GetMethodID(env, cls, "<init>", "(I[B[BIII[B[I[B[I[I[B[B)V");
	if (ctor == NULL)
		return NULL;
	return (*env)->NewObject(env, cls, ctor,
							 (jint) sjni_pq.resultStatus(res), command_status, command_tuples,
							 (jint) sjni_pq.oidValue(res), (jint) cols, (jint) rows,
							 names, meta, data, ends, param_types, error_fields, error_message);
}

jobject
sjni_result_to_java(JNIEnv *env, sjni_conn *conn, sjni_pgresult *res)
{
	jobject		result;

	if (res == NULL)
	{
		sjni_throw_conn(env, conn, "the command");
		return NULL;
	}
	result = build(env, res);
	sjni_pq.clear(res);
	return result;
}

JNIEXPORT jobject JNICALL
Java_org_stuntgres_pq_ResultCopy_exec0(JNIEnv *env, jclass cls, jlong handle, jbyteArray jsql)
{
	sjni_conn  *conn = sjni_conn_of(env, handle);
	char	   *sql;
	sjni_pgresult *res;

	(void) cls;
	if (conn == NULL)
		return NULL;
	if ((sql = sjni_dup_bytes(env, jsql, NULL)) == NULL)
		return NULL;
	res = sjni_pq.exec(conn->pg, sql);
	free(sql);
	return sjni_result_to_java(env, conn, res);
}
```

```java
package org.stuntgres.pq;

/** The native entry that runs a statement string and copies its last result. */
final class ResultCopy {
    private ResultCopy() {
    }

    /**
     * Runs a statement string as {@code PQexec} does and returns the copied result.
     *
     * @param handle a connection handle, not 0
     * @param sql the UTF-8 statement text without a zero byte
     * @return the last result, or the first error result
     * @throws PqException kind TOO_LARGE, OUT_OF_MEMORY or CALL_FAILED
     */
    static native PqResult exec0(long handle, byte[] sql) throws PqException;
}
```

### 6.3 Limits

| Limit | Value | What happens |
|---|---|---|
| values of one result | 2,147,483,639 bytes (`SJNI_MAX_ARRAY`) | `PqException` TOO_LARGE (text in 6.2); the result is cleared; the connection stays usable. |
| cells of one result | 2,147,483,639 | the same, with the other text. |
| one value | the same as all values | follows from the first row. |
| Java heap too small for the arrays | — | the JVM's `OutOfMemoryError`; the result is cleared. |

Not exercised: a result beyond 2 GiB (the checks are plain 64-bit comparisons before
any allocation). Exercised **(measured)**: a 10 MiB value in a result (30 ms on
PostgreSQL, 107 ms on the stuntgres server, read back byte for byte, the column after it
intact) and a 10 MiB parameter round trip (52 ms and 125 ms).

### 6.4 Cost against a call per value

**(measured, experiment 6)** 10,000 rows of 10 columns (int, text, md5, int8, bool, a
column that is NULL in every third row, timestamptz, numeric, a string of 0 to 49
characters, an int array): 1,450,610 bytes. Median of 30 rounds after warm-up, oracle
mode, without `-Xcheck:jni`:

| | JDK 17.0.13 | JDK 21.0.6 |
|---|---|---|
| `PQexec` and `PQclear` alone | 11.76 ms | 11.98 ms |
| the designed call (`exec`, copy, `clear`) | 12.34 ms: the copy costs 0.58 ms | 12.54 ms: 0.56 ms |
| one JNI call per value returning a `byte[]` | 4.05 ms | 3.87 ms |
| three JNI calls per value (is null, length, value) | 5.11 ms | 5.00 ms |
| decoding all values to `String` afterwards | 1.44 ms from the copy, 1.49 ms from single arrays | 1.48 ms, 1.43 ms |

The single copy is seven to nine times cheaper than calls per value and leaves one
object graph of five arrays instead of 100,000 small ones. The fixed point stands.


## 7. Connection wrapper, notices, cancel, notifications, COPY

### 7.1 The C wrapper of a connection

A Java connection handle is the address of an `sjni_conn` (declared in `sjni.h`): the
library's connection pointer and the notice queue. `connect0` allocates it, `finish0`
frees it; nothing else does. It has no lock: one thread at a time uses a connection
(docs/LIBSPQ.md section 7 rule 5), and Java guarantees it (9.1).

### 7.2 Notices (`sjni_notice.c`: stub in PQ-04A, final in PQ-08)

The library reports a notice or warning by calling the receiver of the connection, on
the thread that is inside a library call for that connection, never on another thread
(docs/LIBSPQ.md 5.6). The shim's receiver does not call into Java. It copies the notice
and appends it to the queue of the `sjni_conn`; Java empties the queue with
`drainNotices0` after a call.

Receiver `receive(void *arg, const sjni_pgresult *res)`:

1. `conn = arg`; `message = resultErrorMessage(res)`; `fields_len =
   sjni_pack_fields(res, NULL)`; `message_len = strlen(message)` (0 for a null pointer).
2. `malloc` one `sjni_notice` with `fields_len + message_len` bytes of data. On failure
   increment `conn->notice_lost` and return (the notice is dropped; nothing else can be
   done inside a callback).
3. Pack the fields into the data, copy the message behind them, append the node to the
   tail of the queue, increment `conn->notice_count`.

`sjni_notice_install(conn)`: `setNoticeReceiver(conn->pg, receive, conn)`.
`sjni_notice_discard(conn)`: free every node, reset head, tail and count.

`drainNotices0(handle)`:

1. `sjni_conn_of`; an empty queue returns null (the common case costs one JNI call and
   no allocation).
2. `FindClass("[B")`; `NewObjectArray(2 * count, ...)`.
3. For each node from the head: make a `byte[]` of the packed fields and one of the
   message, store them at `2i` and `2i + 1`, delete the two local references, unlink
   and free the node. If an array cannot be made, return null with the exception
   pending; the nodes not yet handed over stay queued.
4. Return the array. `ConnNotices.takeNotices` turns each pair into a `PqNotice`.

The queue is not bounded: a caller that never drains keeps every notice until the
connection is closed. The JDBC layer drains after every execution (JDBC-DESIGN-CORE).

**Notices while the session starts** **(measured, experiment 10)**. With a blocking
connect a receiver can only be installed after the connection exists, and a notice the
server sends during session start goes to the library's default hook, which prints it:
on PostgreSQL 18.6 a role setting that is invalid at session start
(`ALTER ROLE ... SET default_text_search_config = 'no_such_config'`) made `PQconnectdb`
write `WARNING:  invalid value for parameter "default_text_search_config":
"no_such_config"` to `stderr`, and `options='-c client_min_messages=debug5'` produced a
`DEBUG:` line there. With the design of 5.2 (`connectStart`, receiver, `connectPoll`)
`stderr` stayed empty in both cases and the notice was the first element of
`takeNotices()` after the connect. pgjdbc reports the same warning through
`Connection.getWarnings()` (SQLSTATE `22023`), so the JDBC layer can do the same. The
stuntgres server sent no notice at session start in these cases.

What remains: libspq completes the connection inside `SPQconnectStart` (docs/LIBSPQ.md
5.1), so a notice that libspq raises while a session starts is delivered through the
default hook, to `stderr`, before the shim can install its receiver. The binding cannot
prevent that; it needs a change in libspq (for example: keep such notices queued until
the first `SPQconnectPoll`). It is reported with this design, not worked around.

The stub of PQ-04A (notices are received and dropped, so nothing is printed) is in
11.2. The complete final file:

```c
/* sjni_notice.c - the notice queue of a connection. */
#include <stdlib.h>
#include <string.h>

#include "sjni.h"
#include "org_stuntgres_pq_ConnNotices.h"

/* Called by the library on the thread that is inside a call for this connection. */
static void
receive(void *arg, const sjni_pgresult *res)
{
	sjni_conn  *conn = arg;
	const char *message = sjni_pq.resultErrorMessage(res);
	size_t		fields_len = sjni_pack_fields(res, NULL);
	size_t		message_len = message ? strlen(message) : 0;
	sjni_notice *notice = malloc(sizeof *notice + fields_len + message_len);

	if (notice == NULL)
	{
		conn->notice_lost++;
		return;
	}
	notice->next = NULL;
	notice->fields_len = (int) fields_len;
	notice->message_len = (int) message_len;
	sjni_pack_fields(res, notice->data);
	if (message_len > 0)
		memcpy(notice->data + fields_len, message, message_len);
	if (conn->notice_tail != NULL)
		conn->notice_tail->next = notice;
	else
		conn->notice_head = notice;
	conn->notice_tail = notice;
	conn->notice_count++;
}

void
sjni_notice_install(sjni_conn *conn)
{
	sjni_pq.setNoticeReceiver(conn->pg, receive, conn);
}

void
sjni_notice_discard(sjni_conn *conn)
{
	while (conn->notice_head != NULL)
	{
		sjni_notice *next = conn->notice_head->next;

		free(conn->notice_head);
		conn->notice_head = next;
	}
	conn->notice_tail = NULL;
	conn->notice_count = 0;
}

JNIEXPORT jobjectArray JNICALL
Java_org_stuntgres_pq_ConnNotices_drainNotices0(JNIEnv *env, jclass cls, jlong handle)
{
	sjni_conn  *conn = sjni_conn_of(env, handle);
	jobjectArray out;
	jclass		bytes_class;
	int			count;

	(void) cls;
	if (conn == NULL || conn->notice_count == 0)
		return NULL;
	count = conn->notice_count;
	if ((bytes_class = (*env)->FindClass(env, "[B")) == NULL)
		return NULL;
	if ((out = (*env)->NewObjectArray(env, 2 * count, bytes_class, NULL)) == NULL)
		return NULL;
	for (int i = 0; i < count; i++)
	{
		sjni_notice *notice = conn->notice_head;
		jbyteArray	fields = sjni_new_bytes(env, notice->data, (size_t) notice->fields_len);
		jbyteArray	message;

		if (fields == NULL)
			return NULL;
		message = sjni_new_bytes(env, notice->data + notice->fields_len,
								 (size_t) notice->message_len);
		if (message == NULL)
			return NULL;
		(*env)->SetObjectArrayElement(env, out, 2 * i, fields);
		(*env)->SetObjectArrayElement(env, out, 2 * i + 1, message);
		(*env)->DeleteLocalRef(env, fields);
		(*env)->DeleteLocalRef(env, message);
		conn->notice_head = notice->next;
		if (conn->notice_head == NULL)
			conn->notice_tail = NULL;
		conn->notice_count--;
		free(notice);
	}
	return out;
}
```

### 7.3 Cancel (`sjni_cancel.c`, PQ-09)

`getCancel0(connection)`: `getCancel(conn)`; a null pointer raises CALL_FAILED
`creating the cancel handle failed: <message>`. `cancel0(cancel)`: `errbuf[256]`;
`cancel(cancel, errbuf, 256)` returns 1 → return null; else return the bytes of
`errbuf`. It takes no connection handle, raises nothing and is the one function that may
run while another thread is inside a call for the same connection. `freeCancel0`: ignore
0, else `freeCancel`.

Behavior **(measured in oracle and server mode)**: a cancel sent while `SELECT
pg_sleep(30)` runs ends that statement with a FATAL_ERROR result, SQLSTATE `57014`,
message `canceling statement due to user request`; the connection is idle and usable
afterwards (`TransactionStatus.IDLE`). A cancel that arrives before the statement
started or when nothing runs has no effect, so a test repeats it until the statement
returns. With a real libpq `cancel` opens a second connection to the server and takes
about a millisecond; with libspq it never blocks. The cancel object outlives its
connection. The complete file:

```c
/* sjni_cancel.c - the cancel handle. */
#include "sjni.h"
#include "org_stuntgres_pq_PqCancel.h"

JNIEXPORT jlong JNICALL
Java_org_stuntgres_pq_PqCancel_getCancel0(JNIEnv *env, jclass cls, jlong handle)
{
	sjni_conn  *conn = sjni_conn_of(env, handle);
	sjni_pgcancel *cancel;

	(void) cls;
	if (conn == NULL)
		return 0;
	cancel = sjni_pq.getCancel(conn->pg);
	if (cancel == NULL)
		sjni_throw_conn(env, conn, "creating the cancel handle");
	return (jlong) (intptr_t) cancel;
}

JNIEXPORT jbyteArray JNICALL
Java_org_stuntgres_pq_PqCancel_cancel0(JNIEnv *env, jclass cls, jlong cancel)
{
	char		errbuf[256];
	size_t		n = 0;

	(void) cls;
	errbuf[0] = '\0';
	if (sjni_pq.cancel((sjni_pgcancel *) (intptr_t) cancel, errbuf, (int) sizeof errbuf))
		return NULL;
	while (errbuf[n] != '\0')
		n++;
	return sjni_new_bytes(env, errbuf, n);
}

JNIEXPORT void JNICALL
Java_org_stuntgres_pq_PqCancel_freeCancel0(JNIEnv *env, jclass cls, jlong cancel)
{
	(void) env;
	(void) cls;
	if (cancel != 0)
		sjni_pq.freeCancel((sjni_pgcancel *) (intptr_t) cancel);
}
```

### 7.4 Notifications (`sjni_notify.c`, PQ-10)

`notifies0(handle, timeoutMillis)`: 0 returns what is there, a positive value waits at
most that long for the first notification, a negative value waits without limit.

1. Loop: `consumeInput(conn)`; 0 → CALL_FAILED `reading notifications failed:
   <message>`. Then take every pending notification with `notifies(conn)` and chain
   them in a C list (the struct's own `next` member).
2. If the list is not empty, or `timeoutMillis` is 0, or the wait already happened:
   leave the loop.
3. Otherwise wait once: `poll()` the descriptor of `socket(conn)` for reading until it
   is readable or the time is over (restart after `EINTR` with the time that is left,
   measured with `CLOCK_MONOTONIC`); an error other than `EINTR` → CALL_FAILED `waiting
   for notifications failed: <strerror>`. Go round the loop once more.
4. Build a `PqNotification[]`: for each list element `new PqNotification(be_pid,
   bytes(relname), bytes(extra))` (constructor signature `(I[B[B)V`).
5. On every path free each list element with `freemem`.

The shim polls the descriptor itself and does not need `socketPoll`. A thread inside a
wait cannot be woken by another thread; callers pass a bound. Behavior **(measured)**:
`LISTEN ch`, then `NOTIFY ch, 'p1'; NOTIFY ch` on the same connection: `notifies(0)`
returns two notifications, channel `ch`, payloads `p1` and the empty string, pid equal
to `backendPid()`; `notifies(200)` with nothing pending returns an empty list after at
least 200 ms; a `NOTIFY` from a second connection is returned by `notifies(10000)` with
the other connection's pid. The complete file:

```c
/* sjni_notify.c - notifications with a timed wait. */
#include <errno.h>
#include <poll.h>
#include <string.h>
#include <time.h>

#include "sjni.h"
#include "org_stuntgres_pq_ConnNotify.h"

static int64_t
now_ms(void)
{
	struct timespec ts;

	clock_gettime(CLOCK_MONOTONIC, &ts);
	return (int64_t) ts.tv_sec * 1000 + ts.tv_nsec / 1000000;
}

/* Wait until fd is readable; 1 readable, 0 timed out, -1 error. timeout < 0: no limit. */
static int
wait_readable(int fd, int64_t timeout_ms)
{
	int64_t		deadline = timeout_ms < 0 ? 0 : now_ms() + timeout_ms;

	for (;;)
	{
		struct pollfd p = {fd, POLLIN, 0};
		int64_t		left = timeout_ms < 0 ? -1 : deadline - now_ms();
		int			rc;

		if (timeout_ms >= 0 && left < 0)
			left = 0;
		rc = poll(&p, 1, left > 0x7FFFFFFF ? 0x7FFFFFFF : (int) left);
		if (rc > 0)
			return 1;
		if (rc == 0)
		{
			if (timeout_ms >= 0 && now_ms() >= deadline)
				return 0;
			continue;
		}
		if (errno != EINTR)
			return -1;
	}
}

JNIEXPORT jobjectArray JNICALL
Java_org_stuntgres_pq_ConnNotify_notifies0(JNIEnv *env, jclass cls, jlong handle,
										   jlong timeout_ms)
{
	sjni_conn  *conn = sjni_conn_of(env, handle);
	sjni_pgnotify *first = NULL;
	sjni_pgnotify *last = NULL;
	sjni_pgnotify *n;
	jobjectArray out = NULL;
	jclass		ncls;
	jmethodID	ctor;
	int			count = 0;
	int			waited = 0;

	(void) cls;
	if (conn == NULL)
		return NULL;
	for (;;)
	{
		if (!sjni_pq.consumeInput(conn->pg))
		{
			sjni_throw_conn(env, conn, "reading notifications");
			goto done;
		}
		while ((n = sjni_pq.notifies(conn->pg)) != NULL)
		{
			n->next = NULL;
			if (last != NULL)
				last->next = n;
			else
				first = n;
			last = n;
			count++;
		}
		if (count > 0 || timeout_ms == 0 || waited)
			break;
		if (wait_readable(sjni_pq.socket(conn->pg), timeout_ms) < 0)
		{
			sjni_throw(env, SJNI_KIND_CALL_FAILED, "waiting for notifications failed: %s",
					   strerror(errno));
			goto done;
		}
		waited = 1;
	}
	if ((ncls = (*env)->FindClass(env, "org/stuntgres/pq/PqNotification")) == NULL)
		goto done;
	if ((ctor = (*env)->GetMethodID(env, ncls, "<init>", "(I[B[B)V")) == NULL)
		goto done;
	if ((out = (*env)->NewObjectArray(env, count, ncls, NULL)) == NULL)
		goto done;
	n = first;
	for (int i = 0; i < count; i++, n = n->next)
	{
		jbyteArray	channel = sjni_new_bytes(env, n->relname, strlen(n->relname));
		jbyteArray	payload;
		jobject		item;

		if (channel == NULL)
			goto fail;
		if ((payload = sjni_new_bytes(env, n->extra, strlen(n->extra))) == NULL)
			goto fail;
		if ((item = (*env)->NewObject(env, ncls, ctor, (jint) n->be_pid, channel, payload)) == NULL)
			goto fail;
		(*env)->SetObjectArrayElement(env, out, i, item);
		(*env)->DeleteLocalRef(env, channel);
		(*env)->DeleteLocalRef(env, payload);
		(*env)->DeleteLocalRef(env, item);
	}
	goto done;

fail:
	out = NULL;
done:
	while (first != NULL)
	{
		n = first->next;
		sjni_pq.freemem(first);
		first = n;
	}
	return out;
}
```

### 7.5 COPY (`sjni_copy.c`, PQ-11)

`putCopyData0(handle, buffer, offset, length)`: allocate `length` bytes, copy the region
(an index outside the array leaves `ArrayIndexOutOfBoundsException` pending: free and
return -1), `putCopyData(conn, copy, length)`, free, return the library's value.
`putCopyEnd0(handle, errorMessage)`: copy the text unless it is null,
`putCopyEnd(conn, text or NULL)`, free, return the value. `getCopyData0(handle)`:
`n = getCopyData(conn, &buffer, 0)` (blocking); -1 → return null (the copy is done);
-2 or a null buffer → CALL_FAILED `reading COPY data failed: <message>`; else a new
array of `n` bytes and `freemem(buffer)`.

The sequence at this level **(measured)**:

- `COPY t FROM STDIN`: `execAll` returns one result with status COPY_IN (two nameless
  columns for a two-column table) and stops; `putCopyData` any number of times;
  `putCopyEnd(null)`; `getResult()` returns COMMAND_OK with tag `COPY 2`, then null.
  `putCopyEnd("stop it")` instead gives a FATAL_ERROR result, SQLSTATE `57014`, message
  `COPY from stdin failed: stop it`, context `COPY t, line 1`.
- `COPY (SELECT ...) TO STDOUT`: `execAll` returns one COPY_OUT result; `getCopyData()`
  returns one row per call including its line feed (`1\tone\n`, NULL as `\N`), then
  null; `getResult()` returns COMMAND_OK with tag `COPY 4`, then null.

The complete file:

```c
/* sjni_copy.c - the three COPY calls. */
#include <stdlib.h>

#include "sjni.h"
#include "org_stuntgres_pq_ConnCopy.h"

JNIEXPORT jint JNICALL
Java_org_stuntgres_pq_ConnCopy_putCopyData0(JNIEnv *env, jclass cls, jlong handle,
											jbyteArray jbuffer, jint offset, jint length)
{
	sjni_conn  *conn = sjni_conn_of(env, handle);
	char	   *buffer;
	int			rc;

	(void) cls;
	if (conn == NULL)
		return -1;
	if ((buffer = sjni_alloc(env, (size_t) length)) == NULL)
		return -1;
	(*env)->GetByteArrayRegion(env, jbuffer, offset, length, (jbyte *) buffer);
	if ((*env)->ExceptionCheck(env))
	{
		free(buffer);
		return -1;
	}
	rc = sjni_pq.putCopyData(conn->pg, buffer, length);
	free(buffer);
	return rc;
}

JNIEXPORT jint JNICALL
Java_org_stuntgres_pq_ConnCopy_putCopyEnd0(JNIEnv *env, jclass cls, jlong handle,
										   jbyteArray jerror)
{
	sjni_conn  *conn = sjni_conn_of(env, handle);
	char	   *error = NULL;
	int			rc;

	(void) cls;
	if (conn == NULL)
		return -1;
	if (jerror != NULL && (error = sjni_dup_bytes(env, jerror, NULL)) == NULL)
		return -1;
	rc = sjni_pq.putCopyEnd(conn->pg, error);
	free(error);
	return rc;
}

JNIEXPORT jbyteArray JNICALL
Java_org_stuntgres_pq_ConnCopy_getCopyData0(JNIEnv *env, jclass cls, jlong handle)
{
	sjni_conn  *conn = sjni_conn_of(env, handle);
	char	   *buffer = NULL;
	jbyteArray	row;
	int			n;

	(void) cls;
	if (conn == NULL)
		return NULL;
	n = sjni_pq.getCopyData(conn->pg, &buffer, 0);
	if (n == -1)
		return NULL;
	if (n < 0 || buffer == NULL)
	{
		sjni_throw_conn(env, conn, "reading COPY data");
		return NULL;
	}
	row = sjni_new_bytes(env, buffer, (size_t) n);
	sjni_pq.freemem(buffer);
	return row;
}
```


## 8. Loader

`org.stuntgres.pq.NativeLoader` finds the two native files and loads them: the shim with
`System.load`, the target library through the shim (4.2). It is the only class that
calls `System.load`. Nothing is loaded while a class is initialized: the first
`PqConnection.connect` (and `PqCluster`, `GateCondition`) calls
`NativeLoader.ensureLoaded()` **(measured: after initializing every class of the package
`/proc/self/maps` shows neither file)**.

### 8.1 Properties

| System property | Kind | Meaning |
|---|---|---|
| `stuntgres.native.dir` | consumer contract | Absolute path of a directory that holds `libstuntgres_jni.so` and `libspq.so`. When set, the class path is not searched. |
| `stuntgres.native.cache` | consumer contract | Directory under which files found on the class path are extracted. Default: 8.3. |
| `stuntgres.test.native.shim` | private test seam | Absolute path of the shim file. When set, the two properties above are ignored and the next two are read. |
| `stuntgres.test.native.library` | private test seam | Absolute paths separated by `:`; all but the last are opened first as dependencies, the last is the target library. |
| `stuntgres.test.native.prefix` | private test seam | Symbol prefix, default `SPQ`. The harness sets `PQ` for a real libpq. |

The three `stuntgres.test.*` properties are not part of the consumer contract and are
not documented outside this file. Native mode of the harness uses them too (10.8).

### 8.2 `ensureLoaded()`

1. If `PqLibrary.isLoaded()`, return. Otherwise take the loader's lock and check again.
   A call after a successful load never looks at the properties again.
2. `platform()`: `os.name` must start with `linux` (case-insensitive) and `os.arch`
   must be `amd64` or `x86_64` (→ `x86_64`) or `aarch64` or `arm64` (→ `aarch64`);
   otherwise raise UNSUPPORTED_PLATFORM `stuntgres supports Linux with glibc on x86_64
   and aarch64 only; this JVM runs on <os.name> <os.arch>`. Then read
   `/proc/self/maps`: a mapped file whose name starts with `ld-musl-` or `libc.musl-`
   means the JVM runs on musl: UNSUPPORTED_PLATFORM `... only; this JVM runs on musl
   libc`. An unreadable map counts as glibc.
3. Choose the files:
   - test seam set: the shim is that path; the libraries are the list (empty: LOAD_FAILED
     `... stuntgres.test.native.library is not set`); the prefix is the property or `SPQ`;
   - else `stuntgres.native.dir` set: it must be absolute (LOAD_FAILED `...
     stuntgres.native.dir=<dir>: the path is not absolute`) and hold both files
     (LOAD_FAILED `... stuntgres.native.dir=<dir>: <file name> not found`);
   - else `NativeExtractor.extract(arch)` (8.3) returns the directory that holds both.
4. Load the shim once per class loader: `System.load(shim)`. On `UnsatisfiedLinkError`
   whose message contains `already loaded in another classloader`: copy the shim to a
   new temporary file (`Files.createTempFile("stuntgres-jni-", ".so")`), `System.load`
   the copy, delete the copy (the mapping survives). Any other link error, or a failure
   of the copy: LOAD_FAILED `cannot load the stuntgres native libraries: <reason>`.
5. `PqLibrary.load(dependencies, library, prefix)` (9.4). Its `PqException` passes
   through with the texts of 4.2.

Every LOAD_FAILED text raised by the loader itself starts with `cannot load the
stuntgres native libraries: `. A failed attempt leaves nothing latched: the next call
tries again with the properties as they are then **(measured)**.

The complete file `NativeLoader.java` (PQ-03A):

```java
package org.stuntgres.pq;

import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.Paths;
import java.nio.file.StandardCopyOption;
import java.util.Arrays;
import java.util.Collections;
import java.util.List;
import java.util.Locale;

/**
 * Finds and loads the two native files: the shim {@code libstuntgres_jni.so} and the
 * target library {@code libspq.so}. The only class that calls {@code System.load}.
 * Nothing is loaded while a class is initialized: the first connection attempt calls
 * {@link #ensureLoaded()}.
 */
public final class NativeLoader {
    /** System property: a directory that holds both native files. */
    public static final String DIR_PROPERTY = "stuntgres.native.dir";
    /** System property: the directory extracted native files are kept in. */
    public static final String CACHE_PROPERTY = "stuntgres.native.cache";

    /** File name of the shim. */
    static final String SHIM_NAME = "libstuntgres_jni.so";
    /** File name of the target library. */
    static final String LIBRARY_NAME = "libspq.so";
    /** Resource that lists the checksums of the native files of one platform. */
    static final String INDEX_NAME = "natives.sha256";

    // Test seam, not part of the consumer contract.
    static final String TEST_SHIM_PROPERTY = "stuntgres.test.native.shim";
    static final String TEST_LIBRARY_PROPERTY = "stuntgres.test.native.library";
    static final String TEST_PREFIX_PROPERTY = "stuntgres.test.native.prefix";

    private static final Object LOCK = new Object();
    private static boolean shimLoaded;

    private NativeLoader() {
    }

    /**
     * Loads the native files unless they are loaded already.
     *
     * @throws PqException kind UNSUPPORTED_PLATFORM or LOAD_FAILED
     */
    public static void ensureLoaded() throws PqException {
        if (PqLibrary.isLoaded()) {
            return;
        }
        synchronized (LOCK) {
            if (PqLibrary.isLoaded()) {
                return;
            }
            String arch = platform();
            String testShim = System.getProperty(TEST_SHIM_PROPERTY);
            String dir = System.getProperty(DIR_PROPERTY);
            Path shim;
            List<String> libraries;
            String prefix = "SPQ";
            if (testShim != null) {
                String list = System.getProperty(TEST_LIBRARY_PROPERTY, "");
                if (list.isEmpty()) {
                    throw failed(TEST_LIBRARY_PROPERTY + " is not set");
                }
                shim = Paths.get(testShim);
                libraries = Arrays.asList(list.split(":"));
                prefix = System.getProperty(TEST_PREFIX_PROPERTY, "SPQ");
            } else if (dir != null) {
                Path base = Paths.get(dir);
                if (!base.isAbsolute()) {
                    throw failed(DIR_PROPERTY + "=" + dir + ": the path is not absolute");
                }
                shim = base.resolve(SHIM_NAME);
                Path library = base.resolve(LIBRARY_NAME);
                for (Path file : new Path[] {shim, library}) {
                    if (!Files.isRegularFile(file)) {
                        throw failed(DIR_PROPERTY + "=" + dir + ": " + file.getFileName()
                            + " not found");
                    }
                }
                libraries = Collections.singletonList(library.toString());
            } else {
                Path base = NativeExtractor.extract(arch);
                shim = base.resolve(SHIM_NAME);
                libraries = Collections.singletonList(base.resolve(LIBRARY_NAME).toString());
            }
            if (!shimLoaded) {
                loadShim(shim);
                shimLoaded = true;
            }
            int last = libraries.size() - 1;
            PqLibrary.load(libraries.subList(0, last), libraries.get(last), prefix);
        }
    }

    static PqException failed(String reason) {
        return new PqException(PqException.LOAD_FAILED,
            "cannot load the stuntgres native libraries: " + reason);
    }

    /**
     * Checks the platform.
     *
     * @return the architecture name used in resource paths: x86_64 or aarch64
     */
    static String platform() throws PqException {
        String os = System.getProperty("os.name", "");
        String cpu = System.getProperty("os.arch", "");
        String arch = null;
        if (cpu.equals("amd64") || cpu.equals("x86_64")) {
            arch = "x86_64";
        } else if (cpu.equals("aarch64") || cpu.equals("arm64")) {
            arch = "aarch64";
        }
        if (!os.toLowerCase(Locale.ROOT).startsWith("linux") || arch == null) {
            throw new PqException(PqException.UNSUPPORTED_PLATFORM,
                "stuntgres supports Linux with glibc on x86_64 and aarch64 only; this JVM"
                    + " runs on " + os + " " + cpu);
        }
        if (usesMusl(Paths.get("/proc/self/maps"))) {
            throw new PqException(PqException.UNSUPPORTED_PLATFORM,
                "stuntgres supports Linux with glibc on x86_64 and aarch64 only; this JVM"
                    + " runs on musl libc");
        }
        return arch;
    }

    /** True when the memory map of the process shows the musl C library. */
    static boolean usesMusl(Path maps) {
        try {
            for (String line : Files.readAllLines(maps, StandardCharsets.ISO_8859_1)) {
                int slash = line.lastIndexOf('/');
                String file = slash < 0 ? "" : line.substring(slash + 1);
                if (file.startsWith("ld-musl-") || file.startsWith("libc.musl-")) {
                    return true;
                }
            }
        } catch (IOException e) {
            return false;
        }
        return false;
    }

    /**
     * Calls System.load for the shim. A second class loader of the same JVM cannot
     * load the same file, so it loads a private copy and deletes it at once.
     */
    private static void loadShim(Path shim) throws PqException {
        try {
            System.load(shim.toString());
        } catch (UnsatisfiedLinkError e) {
            String message = String.valueOf(e.getMessage());
            if (!message.contains("already loaded in another classloader")) {
                throw new PqException(PqException.LOAD_FAILED,
                    "cannot load the stuntgres native libraries: " + message, e);
            }
            Path copy = null;
            try {
                copy = Files.createTempFile("stuntgres-jni-", ".so");
                Files.copy(shim, copy, StandardCopyOption.REPLACE_EXISTING);
                System.load(copy.toString());
            } catch (IOException | UnsatisfiedLinkError e2) {
                throw new PqException(PqException.LOAD_FAILED,
                    "cannot load the stuntgres native libraries: " + e2.getMessage(), e2);
            } finally {
                if (copy != null) {
                    try {
                        Files.deleteIfExists(copy);
                    } catch (IOException ignored) {
                        // The copy stays in the temporary directory.
                    }
                }
            }
        }
    }
}
```

### 8.3 Extraction from the class path (`NativeExtractor`, PQ-03B)

A jar that carries native files has them under `META-INF/native/linux-<arch>/`
(`<arch>` is `x86_64` or `aarch64`): `libstuntgres_jni.so`, `libspq.so` and
`natives.sha256`, the output of `sha256sum` for the two files (64 hexadecimal digits,
two spaces or a space and `*`, the file name; one line per file). `make jar` writes the
directory (3.2). In this foundation no jar contains a real `libspq.so`; the tests use
the stand-in of 10.9, and JDBC-DESIGN-FEATURES builds the real jar.

`extract(arch)`:

1. Read `/META-INF/native/linux-<arch>/natives.sha256` with
   `NativeExtractor.class.getResourceAsStream`. Absent: LOAD_FAILED `... no native files
   for linux-<arch> on the class path (META-INF/native/linux-<arch>/natives.sha256) and
   stuntgres.native.dir is not set`. A file name it does not list: `... natives.sha256
   does not list <name>`.
2. Choose the base directory, the first of these that can be created (mode `rwx------`)
   and is a private, writable directory of this user (owner equal to `user.name`, no
   write permission for group or others):
   `stuntgres.native.cache` when set (then only that); else `$XDG_CACHE_HOME/stuntgres-jdbc`
   when the variable is an absolute path; `${user.home}/.cache/stuntgres-jdbc`;
   `${java.io.tmpdir}/stuntgres-jdbc-${user.name}`. None: LOAD_FAILED `... no directory
   to extract them to (<last reason>); set stuntgres.native.cache or
   stuntgres.native.dir`.
3. The directory of this version is `<base>/<first 32 hexadecimal digits of the SHA-256
   of the bytes of natives.sha256>`: a name derived from the content of both files.
4. For each of the two files: if it exists there as a regular file, use it. Otherwise
   copy the resource to a temporary file in the same directory while computing its
   SHA-256; a mismatch with the list: LOAD_FAILED `... META-INF/native/linux-<arch>/<name>
   does not match its checksum`; set the mode `r-x------`; publish it with a hard link
   under the final name (`Files.createLink`), which fails when the name exists and never
   replaces a file; delete the temporary file. Losing the race to another thread or JVM
   is success: the file that is there has the same content.
5. Set the directory's modification time to now. When the directory was created by this
   call, remove sibling directories whose modification time is older than 30 days (their
   files first); errors while removing are ignored.
6. Return the directory.

Why a link and never a replace: all class loaders of one JVM must open the same inode of
`libspq.so`, otherwise the process would hold two copies of the engine (4.2 step 5
would refuse the second).

Measured **(experiments 2 and 8)**:

| Case | Observation |
|---|---|
| a 94.7 MB library (39.8 MB in the jar) and the shim, cache absent | first JVM 0.43 s in total (start, extraction, checksum, `dlopen`); the directory has mode `rwx------`, the files `r-x------` |
| the same, cache filled | 0.06 s; the files are not rewritten (same inode) |
| eight JVMs extracting into an empty cache at once | all load; the directory holds exactly the two files afterwards |
| a resource whose bytes do not match the list | LOAD_FAILED with the checksum text; nothing is published |
| a cache directory that exists with group write permission | refused: `<dir> is not a private directory of this user` |
| the jar of `make jar` alone on the class path | both files extracted and loaded |
| two class loaders | section 4.2 |
| `-Dos.name="Mac OS X" -Dos.arch=aarch64`; `-Dos.arch=riscv64` | UNSUPPORTED_PLATFORM with the texts of 8.2 |

Not measured: a JVM on musl (none is installed; the detection is tested on a made-up
memory map), `aarch64`, a file system without POSIX permissions, a temporary directory
mounted `noexec` (then the private copy of step 4 of 8.2 cannot be loaded and the second
class loader gets LOAD_FAILED with the JVM's message).

The stub of PQ-03A is in 11.2. The complete final file `NativeExtractor.java`:

```java
package org.stuntgres.pq;

import java.io.ByteArrayInputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.nio.charset.StandardCharsets;
import java.nio.file.DirectoryStream;
import java.nio.file.FileAlreadyExistsException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.Paths;
import java.nio.file.attribute.FileTime;
import java.nio.file.attribute.PosixFilePermission;
import java.nio.file.attribute.PosixFilePermissions;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.concurrent.TimeUnit;

/**
 * Copies the native files of the class path (META-INF/native/linux-ARCH/) to a
 * private directory named after their checksums, once per version.
 */
final class NativeExtractor {
    private static final long STALE_MILLIS = TimeUnit.DAYS.toMillis(30);

    private NativeExtractor() {
    }

    /** Reads the checksum list: file name to lower-case hexadecimal SHA-256. */
    static Map<String, String> readIndex(InputStream in) throws IOException {
        Map<String, String> sums = new HashMap<>();
        String text = new String(in.readAllBytes(), StandardCharsets.US_ASCII);
        for (String line : text.split("\n")) {
            int space = line.indexOf(' ');
            if (space == 64) {
                sums.put(line.substring(space).trim().replace("*", ""), line.substring(0, 64));
            }
        }
        return sums;
    }

    /** The directory extracted files live in: user-private, created when missing. */
    static Path cacheBase() throws PqException {
        List<Path> candidates = new ArrayList<>();
        String explicit = System.getProperty(NativeLoader.CACHE_PROPERTY);
        if (explicit != null) {
            candidates.add(Paths.get(explicit));
        } else {
            String xdg = System.getenv("XDG_CACHE_HOME");
            if (xdg != null && xdg.startsWith("/")) {
                candidates.add(Paths.get(xdg, "stuntgres-jdbc"));
            }
            candidates.add(Paths.get(System.getProperty("user.home", "/"), ".cache",
                "stuntgres-jdbc"));
            candidates.add(Paths.get(System.getProperty("java.io.tmpdir", "/tmp"),
                "stuntgres-jdbc-" + System.getProperty("user.name", "user")));
        }
        String reason = "";
        for (Path dir : candidates) {
            try {
                Files.createDirectories(dir,
                    PosixFilePermissions.asFileAttribute(
                        PosixFilePermissions.fromString("rwx------")));
                Set<PosixFilePermission> perms = Files.getPosixFilePermissions(dir);
                if (perms.contains(PosixFilePermission.GROUP_WRITE)
                        || perms.contains(PosixFilePermission.OTHERS_WRITE)
                        || !Files.getOwner(dir).getName().equals(
                            System.getProperty("user.name"))) {
                    reason = dir + " is not a private directory of this user";
                    continue;
                }
                if (Files.isWritable(dir)) {
                    return dir;
                }
                reason = dir + " is not writable";
            } catch (IOException | UnsupportedOperationException e) {
                reason = dir + ": " + e;
            }
        }
        throw NativeLoader.failed("no directory to extract them to (" + reason + "); set "
            + NativeLoader.CACHE_PROPERTY + " or " + NativeLoader.DIR_PROPERTY);
    }

    /** Extracts the native files of the class path and returns their directory. */
    static Path extract(String arch) throws PqException {
        String prefix = "/META-INF/native/linux-" + arch + "/";
        Map<String, String> sums;
        byte[] index;
        try (InputStream in =
                NativeExtractor.class.getResourceAsStream(prefix + NativeLoader.INDEX_NAME)) {
            if (in == null) {
                throw NativeLoader.failed("no native files for linux-" + arch
                    + " on the class path (META-INF/native/linux-" + arch + "/"
                    + NativeLoader.INDEX_NAME + ") and " + NativeLoader.DIR_PROPERTY
                    + " is not set");
            }
            index = in.readAllBytes();
            sums = readIndex(new ByteArrayInputStream(index));
        } catch (IOException e) {
            throw NativeLoader.failed("reading " + NativeLoader.INDEX_NAME + ": "
                + e.getMessage());
        }
        for (String name : new String[] {NativeLoader.SHIM_NAME, NativeLoader.LIBRARY_NAME}) {
            if (!sums.containsKey(name)) {
                throw NativeLoader.failed(NativeLoader.INDEX_NAME + " does not list " + name);
            }
        }
        Path base = cacheBase();
        Path dir = base.resolve(hex(sha256().digest(index)).substring(0, 32));
        try {
            boolean fresh = !Files.isDirectory(dir);
            Files.createDirectories(dir);
            for (String name : new String[] {NativeLoader.SHIM_NAME, NativeLoader.LIBRARY_NAME}) {
                extractFile(prefix + name, dir.resolve(name), sums.get(name));
            }
            Files.setLastModifiedTime(dir, FileTime.fromMillis(System.currentTimeMillis()));
            if (fresh) {
                sweep(base, dir);
            }
        } catch (IOException e) {
            throw NativeLoader.failed("extracting to " + dir + ": " + e);
        }
        return dir;
    }

    /** Writes one resource to target unless target exists; never replaces a file. */
    private static void extractFile(String resource, Path target, String sum)
            throws IOException, PqException {
        if (Files.isRegularFile(target)) {
            return;
        }
        Path tmp = Files.createTempFile(target.getParent(), target.getFileName().toString(),
            ".tmp");
        try {
            MessageDigest digest = sha256();
            try (InputStream in = NativeExtractor.class.getResourceAsStream(resource);
                    OutputStream out = Files.newOutputStream(tmp)) {
                if (in == null) {
                    throw NativeLoader.failed(resource.substring(1)
                        + " is not on the class path");
                }
                byte[] buffer = new byte[1 << 16];
                for (int n = in.read(buffer); n > 0; n = in.read(buffer)) {
                    digest.update(buffer, 0, n);
                    out.write(buffer, 0, n);
                }
            }
            if (!hex(digest.digest()).equals(sum)) {
                throw NativeLoader.failed(resource.substring(1)
                    + " does not match its checksum");
            }
            Files.setPosixFilePermissions(tmp, PosixFilePermissions.fromString("r-x------"));
            try {
                Files.createLink(target, tmp);
            } catch (FileAlreadyExistsException e) {
                // Another thread or JVM extracted the same file first; use that one.
            }
        } finally {
            Files.deleteIfExists(tmp);
        }
    }

    /** Removes directories of other versions that nobody used for 30 days. */
    private static void sweep(Path base, Path keep) {
        long limit = System.currentTimeMillis() - STALE_MILLIS;
        try (DirectoryStream<Path> entries = Files.newDirectoryStream(base)) {
            for (Path entry : entries) {
                if (entry.equals(keep) || !Files.isDirectory(entry)
                        || Files.getLastModifiedTime(entry).toMillis() > limit) {
                    continue;
                }
                try (DirectoryStream<Path> files = Files.newDirectoryStream(entry)) {
                    for (Path file : files) {
                        Files.deleteIfExists(file);
                    }
                }
                Files.deleteIfExists(entry);
            }
        } catch (IOException e) {
            // Old directories stay; nothing depends on their removal.
        }
    }

    private static MessageDigest sha256() {
        try {
            return MessageDigest.getInstance("SHA-256");
        } catch (NoSuchAlgorithmException e) {
            throw new IllegalStateException(e);
        }
    }

    private static String hex(byte[] bytes) {
        StringBuilder text = new StringBuilder(bytes.length * 2);
        for (byte b : bytes) {
            text.append(Character.forDigit((b >> 4) & 15, 16));
            text.append(Character.forDigit(b & 15, 16));
        }
        return text.toString();
    }
}
```


## 9. `org.stuntgres.pq`

The Java face of the shim and the only package that declares native methods. Nothing
outside it knows which library is loaded except through `PqLibrary.hasFeature`. It is
not consumer API: the JDBC classes are built on it.

### 9.1 Conventions

- **Complete files.** Every class of the package is given below as its complete file;
  an implementation part writes exactly that file. The tables in front of the files
  summarize names and behavior.
- **One thread at a time.** The methods of a connection take no lock. The caller (the
  JDBC connection, plan B10) guarantees that at most one thread is inside a method of
  one `PqConnection`. The one exception is `PqCancel.cancel()`, on its own object, which
  may run while another thread is inside a call of the connection.
- **Handles.** A native handle is a `long` held in an `AtomicLong` by a small state
  object, which is also the release action. `close()` runs the action; the action swaps
  the handle to 0 and frees it only when it was not 0, so closing twice, closing after
  the collector did, and the collector after `close()` each free exactly once.
- **Reachability.** Every method that passes a handle to a native method has the shape

  ```java
  try {
      return nativeMethod0(handle(), ...);
  } finally {
      Reference.reachabilityFence(this);
  }
  ```

  Without the fence the JVM may treat the object as unreachable while the native call
  runs (its last use is the read of the handle) and let the cleaner free the handle
  under the call. With it the object is strongly reachable until the call has returned.
  **(measured: a statement on a connection that nothing else referred to completed
  while another thread forced more than 20 collections.)**
- **Text.** Strings cross the boundary as UTF-8 `byte[]`. `PqText.cstring` encodes: a
  U+0000 raises BAD_ARGUMENT `<what> contains a NUL character` (the library would cut
  the text there); an unpaired surrogate becomes `?`, which is what pgjdbc sends
  **(measured)**. `PqText.decode` decodes: a malformed sequence becomes U+FFFD; raw
  bytes stay available through `PqResult.bytes`. Bytes that are not UTF-8 only arrive
  when the client encoding was changed **(measured with `LATIN1`: `e9` decodes to
  U+FFFD)**; the JDBC layer keeps `client_encoding` at `UTF8` (plan B5).
- **Messages.** Library messages end with a line feed; Java strips trailing line feeds
  (`PqText.trimNewlines`) before putting them into an exception or returning them.
- **Javadoc and style** as rule R6 of the implementation parts requires; the files below
  comply (lines of at most 100 columns, Javadoc on public types and methods).
- **Lint.** The sources compile with `-Xlint:all -Werror` on JDK 17 and 21. A trap
  **(measured)**: calling `close()` explicitly on a variable of a try-with-resources
  statement is a warning (`[try] explicit call to close() on an auto-closeable
  resource`); tests of idempotent close use a plain variable.

### 9.2 Constants (PQ-01A)

Constants are `public static final int` fields marked `@Native`, so that `javac -h`
writes them into a header and `sjni_check.c` can compare them with the shim's values.

| Class | Constants |
|---|---|
| `ConnStatus` | `OK` 0, `BAD` 1; `name(int)` → `CONNECTION_OK`, `CONNECTION_BAD`, `CONNECTION_<n>` |
| `ExecStatus` | `EMPTY_QUERY` 0, `COMMAND_OK` 1, `TUPLES_OK` 2, `COPY_OUT` 3, `COPY_IN` 4, `BAD_RESPONSE` 5, `NONFATAL_ERROR` 6, `FATAL_ERROR` 7, `COPY_BOTH` 8, `SINGLE_TUPLE` 9, `PIPELINE_SYNC` 10, `PIPELINE_ABORTED` 11, `TUPLES_CHUNK` 12; `name(int)` → `PGRES_EMPTY_QUERY` ... (the strings `resStatus` returns), `PGRES_<n>` for other values |
| `TransactionStatus` | `IDLE` 0, `ACTIVE` 1, `INTRANS` 2, `INERROR` 3, `UNKNOWN` 4; `name(int)` → `PQTRANS_IDLE` ... |
| `DiagField` | `SEVERITY` 'S', `SEVERITY_NONLOCALIZED` 'V', `SQLSTATE` 'C', `MESSAGE_PRIMARY` 'M', `MESSAGE_DETAIL` 'D', `MESSAGE_HINT` 'H', `STATEMENT_POSITION` 'P', `INTERNAL_POSITION` 'p', `INTERNAL_QUERY` 'q', `CONTEXT` 'W', `SCHEMA_NAME` 's', `TABLE_NAME` 't', `COLUMN_NAME` 'c', `DATATYPE_NAME` 'd', `CONSTRAINT_NAME` 'n', `SOURCE_FILE` 'F', `SOURCE_LINE` 'L', `SOURCE_FUNCTION` 'R' |
| `Feature` | `CLOSE_PREPARED` 0, `CLOSE_PORTAL` 1, `SOCKET_POLL` 2, `CURRENT_TIME_USEC` 3, `CHUNKED_ROWS` 4, `EXT_VERSION` 5, `EXT_CLUSTER_DROP` 6, `EXT_RESET` 7, `EXT_DUMP` 8, `EXT_RESTORE` 9, `COUNT` 10; `name(int)` → the member name of 4.3 (`closePrepared`, ..., `Xrestore`) |

The values are those of CABI 2.2 and 2.3. The complete files:

```java
package org.stuntgres.pq;

import java.lang.annotation.Native;

/** Connection status values of the library ({@code SPQConnStatusType}). */
public final class ConnStatus {
    /** The connection is usable. */
    @Native public static final int OK = 0;
    /** The connection attempt failed or the connection was lost. */
    @Native public static final int BAD = 1;

    private ConnStatus() {
    }

    /**
     * Returns the libpq name of a status value.
     *
     * @param status a status value
     * @return {@code "CONNECTION_OK"}, {@code "CONNECTION_BAD"} or {@code "CONNECTION_<n>"}
     */
    public static String name(int status) {
        switch (status) {
            case OK:
                return "CONNECTION_OK";
            case BAD:
                return "CONNECTION_BAD";
            default:
                return "CONNECTION_" + status;
        }
    }
}
```

```java
package org.stuntgres.pq;

import java.lang.annotation.Native;

/** Result status values of the library ({@code SPQExecStatusType}). */
public final class ExecStatus {
    /** The statement string was empty. */
    @Native public static final int EMPTY_QUERY = 0;
    /** A command that returns no rows completed. */
    @Native public static final int COMMAND_OK = 1;
    /** A command that returns rows completed. */
    @Native public static final int TUPLES_OK = 2;
    /** Copy out of the server has started. */
    @Native public static final int COPY_OUT = 3;
    /** Copy into the server has started. */
    @Native public static final int COPY_IN = 4;
    /** The server's response was not understood. */
    @Native public static final int BAD_RESPONSE = 5;
    /** A notice or warning. */
    @Native public static final int NONFATAL_ERROR = 6;
    /** The command failed. */
    @Native public static final int FATAL_ERROR = 7;
    /** Copy in both directions has started. */
    @Native public static final int COPY_BOTH = 8;
    /** One row of a result fetched in single-row mode. */
    @Native public static final int SINGLE_TUPLE = 9;
    /** Pipeline synchronization point. */
    @Native public static final int PIPELINE_SYNC = 10;
    /** A command skipped because the pipeline was aborted. */
    @Native public static final int PIPELINE_ABORTED = 11;
    /** A chunk of rows of a result fetched in chunked mode. */
    @Native public static final int TUPLES_CHUNK = 12;

    private static final String[] NAMES = {
        "PGRES_EMPTY_QUERY", "PGRES_COMMAND_OK", "PGRES_TUPLES_OK", "PGRES_COPY_OUT",
        "PGRES_COPY_IN", "PGRES_BAD_RESPONSE", "PGRES_NONFATAL_ERROR", "PGRES_FATAL_ERROR",
        "PGRES_COPY_BOTH", "PGRES_SINGLE_TUPLE", "PGRES_PIPELINE_SYNC",
        "PGRES_PIPELINE_ABORTED", "PGRES_TUPLES_CHUNK",
    };

    private ExecStatus() {
    }

    /**
     * Returns the libpq name of a status value.
     *
     * @param status a status value
     * @return for example {@code "PGRES_TUPLES_OK"}; {@code "PGRES_<n>"} for an unknown value
     */
    public static String name(int status) {
        return status >= 0 && status < NAMES.length ? NAMES[status] : "PGRES_" + status;
    }
}
```

```java
package org.stuntgres.pq;

import java.lang.annotation.Native;

/** Transaction status values of the library ({@code SPGTransactionStatusType}). */
public final class TransactionStatus {
    /** Idle, not in a transaction block. */
    @Native public static final int IDLE = 0;
    /** A command is in progress. */
    @Native public static final int ACTIVE = 1;
    /** Idle, in a transaction block. */
    @Native public static final int INTRANS = 2;
    /** Idle, in a failed transaction block. */
    @Native public static final int INERROR = 3;
    /** The connection is bad. */
    @Native public static final int UNKNOWN = 4;

    private static final String[] NAMES = {
        "PQTRANS_IDLE", "PQTRANS_ACTIVE", "PQTRANS_INTRANS", "PQTRANS_INERROR", "PQTRANS_UNKNOWN",
    };

    private TransactionStatus() {
    }

    /**
     * Returns the libpq name of a status value.
     *
     * @param status a status value
     * @return for example {@code "PQTRANS_IDLE"}; {@code "PQTRANS_<n>"} for an unknown value
     */
    public static String name(int status) {
        return status >= 0 && status < NAMES.length ? NAMES[status] : "PQTRANS_" + status;
    }
}
```

```java
package org.stuntgres.pq;

import java.lang.annotation.Native;

/** Error and notice field codes ({@code SPG_DIAG_*}). */
public final class DiagField {
    /** Severity, possibly localized. */
    @Native public static final int SEVERITY = 'S';
    /** Severity, never localized. */
    @Native public static final int SEVERITY_NONLOCALIZED = 'V';
    /** SQLSTATE code. */
    @Native public static final int SQLSTATE = 'C';
    /** Primary message. */
    @Native public static final int MESSAGE_PRIMARY = 'M';
    /** Detail message. */
    @Native public static final int MESSAGE_DETAIL = 'D';
    /** Hint. */
    @Native public static final int MESSAGE_HINT = 'H';
    /** Error position in the statement, counted in characters from 1. */
    @Native public static final int STATEMENT_POSITION = 'P';
    /** Error position in the internal query. */
    @Native public static final int INTERNAL_POSITION = 'p';
    /** Text of the internal query. */
    @Native public static final int INTERNAL_QUERY = 'q';
    /** Context lines. */
    @Native public static final int CONTEXT = 'W';
    /** Schema name. */
    @Native public static final int SCHEMA_NAME = 's';
    /** Table name. */
    @Native public static final int TABLE_NAME = 't';
    /** Column name. */
    @Native public static final int COLUMN_NAME = 'c';
    /** Data type name. */
    @Native public static final int DATATYPE_NAME = 'd';
    /** Constraint name. */
    @Native public static final int CONSTRAINT_NAME = 'n';
    /** Source file of the server. */
    @Native public static final int SOURCE_FILE = 'F';
    /** Source line of the server. */
    @Native public static final int SOURCE_LINE = 'L';
    /** Source function of the server. */
    @Native public static final int SOURCE_FUNCTION = 'R';

    private DiagField() {
    }
}
```

```java
package org.stuntgres.pq;

import java.lang.annotation.Native;

/** Optional functions of the loaded library; see {@link PqLibrary#hasFeature(int)}. */
public final class Feature {
    /** {@code closePrepared}. */
    @Native public static final int CLOSE_PREPARED = 0;
    /** {@code closePortal}. */
    @Native public static final int CLOSE_PORTAL = 1;
    /** {@code socketPoll}. */
    @Native public static final int SOCKET_POLL = 2;
    /** {@code getCurrentTimeUSec}. */
    @Native public static final int CURRENT_TIME_USEC = 3;
    /** {@code setChunkedRowsMode}. */
    @Native public static final int CHUNKED_ROWS = 4;
    /** {@code SPQXversion}. */
    @Native public static final int EXT_VERSION = 5;
    /** {@code SPQXclusterDrop}. */
    @Native public static final int EXT_CLUSTER_DROP = 6;
    /** {@code SPQXreset}. */
    @Native public static final int EXT_RESET = 7;
    /** {@code SPQXdump}. */
    @Native public static final int EXT_DUMP = 8;
    /** {@code SPQXrestore}. */
    @Native public static final int EXT_RESTORE = 9;
    /** Number of features. */
    @Native public static final int COUNT = 10;

    private static final String[] NAMES = {
        "closePrepared", "closePortal", "socketPoll", "getCurrentTimeUSec",
        "setChunkedRowsMode", "Xversion", "XclusterDrop", "Xreset", "Xdump", "Xrestore",
    };

    private Feature() {
    }

    /**
     * Returns the function name of a feature without the library prefix.
     *
     * @param feature a constant of this class
     * @return for example {@code "closePrepared"}
     */
    public static String name(int feature) {
        return NAMES[feature];
    }
}
```

### 9.3 Exception, text helpers and value classes (PQ-01B)

`PqException extends Exception` (checked, final): the one exception type of the
boundary. Server errors are not exceptions at this level; they are results with error
fields. It has no SQLSTATE: the JDBC layer maps kinds to SQLSTATE values.

| Kind | Value | Raised when | Message texts |
|---|---|---|---|
| `NOT_LOADED` | 1 | a native method runs before a library is loaded | `the native library is not loaded` |
| `LOAD_FAILED` | 2 | the files cannot be found, loaded or used | 4.2, 8.2, 8.3 |
| `UNSUPPORTED_PLATFORM` | 3 | not Linux, not glibc, not x86_64 or aarch64 | 8.2 |
| `OUT_OF_MEMORY` | 4 | a C allocation failed, or the library returned no connection object | `out of memory in the native shim (<n> bytes)`; `out of memory: the library returned no connection object` |
| `CLOSED` | 5 | a method is called on a closed connection or cancel handle | `the connection is closed`; `the cancel handle is closed` |
| `BAD_ARGUMENT` | 6 | an argument cannot be passed to the library | `<what> contains a NUL character`; `too many parameters: <n> (the limit is 65535)`; `got <n> parameter types for <m> values`; `got <n> parameter formats for <m> values`; `parameter $<i> has format <f> (0 or 1 expected)` |
| `TOO_LARGE` | 7 | a result or value does not fit in a Java array | 6.2; `a value of <n> bytes does not fit in a Java array` |
| `CALL_FAILED` | 8 | a library function reported failure and there is no result | `<what> failed: <library message>`, the `<what>` texts are in 9.5 to 9.7 |
| `CONNECT_FAILED` | 9 | a connection attempt failed | the library's message unchanged, for example `connection to server at "127.0.0.1", port 5432 failed: FATAL:  database "nope" does not exist`; `missing "=" after "this" in connection info string`; `timeout expired` |
| `UNSUPPORTED` | 10 | the loaded library lacks an optional function | `the loaded library does not provide <prefix><member>` |

`<what>` in the NUL text is one of: `the connection string`, `the statement`, `the
statement name`, `the parameter name`, `parameter $<i>`, `the text`, `the identifier`,
`the encoding name`, `the error message`, `the host`, `the port`, `a library path`, `the
library path`.

Kinds 1 to 8 are `@Native` (C raises them); 9 and 10 are raised by Java only. The texts
are this driver's own wording, except the library messages they pass through.

A failed connection attempt carries only the library's text: neither libpq nor libspq
exposes the SQLSTATE of a refused connection through this API. JDBC-DESIGN-CORE decides
how the JDBC layer derives one.

| Class | Members |
|---|---|
| `PqException` | `PqException(int kind, String message)`, `PqException(int kind, String message, Throwable cause)`, package-private `PqException(int kind, byte[] utf8Message)` (called from C), `int kind()` |
| `PqText` (package-private) | `static byte[] cstring(String text, String what) throws PqException`, `static void checkNoNul(byte[] value, String what) throws PqException`, `static String decode(byte[] bytes, int offset, int length)`, `static String field(byte[] packed, int code)`, `static String trimNewlines(String text)` |
| `Conninfo` | `Conninfo()`, `Conninfo add(String keyword, String value)`, `String toString()`: builds `keyword='value'` pairs separated by one space, with `'` and `\` in a value preceded by `\` |
| `PqNotice` | package-private `PqNotice(byte[] fields, byte[] message)`, `String field(int code)`, `String message()`, `String toString()` (the message) |
| `PqNotification` | package-private `PqNotification(int backendPid, byte[] channel, byte[] payload)` (called from C), `int backendPid()`, `String channel()`, `String payload()`, `String toString()` |

The complete files:

```java
package org.stuntgres.pq;

import java.lang.annotation.Native;

/**
 * A failure of the native boundary itself. Errors the server reports for a statement
 * are not exceptions at this level: they are {@link PqResult} objects with error fields.
 */
public final class PqException extends Exception {
    /** No native library is loaded. */
    @Native public static final int NOT_LOADED = 1;
    /** The native library could not be found or loaded. */
    @Native public static final int LOAD_FAILED = 2;
    /** The platform is not supported. */
    @Native public static final int UNSUPPORTED_PLATFORM = 3;
    /** Native memory is exhausted. */
    @Native public static final int OUT_OF_MEMORY = 4;
    /** The connection or handle is closed. */
    @Native public static final int CLOSED = 5;
    /** An argument cannot be passed to the library. */
    @Native public static final int BAD_ARGUMENT = 6;
    /** A value or result does not fit in a Java array. */
    @Native public static final int TOO_LARGE = 7;
    /** A library function reported failure without a result. */
    @Native public static final int CALL_FAILED = 8;
    /** The connection attempt failed. */
    public static final int CONNECT_FAILED = 9;
    /** The loaded library lacks an optional function. */
    public static final int UNSUPPORTED = 10;

    private static final long serialVersionUID = 1L;

    private final int kind;

    /**
     * Creates an exception.
     *
     * @param kind one of the constants of this class
     * @param message the message
     */
    public PqException(int kind, String message) {
        super(message);
        this.kind = kind;
    }

    /**
     * Creates an exception with a cause.
     *
     * @param kind one of the constants of this class
     * @param message the message
     * @param cause the cause
     */
    public PqException(int kind, String message, Throwable cause) {
        super(message, cause);
        this.kind = kind;
    }

    /** Called by the shim: the message arrives as UTF-8 bytes. */
    PqException(int kind, byte[] utf8Message) {
        this(kind, PqText.decode(utf8Message, 0, utf8Message.length));
    }

    /**
     * Returns the kind of failure.
     *
     * @return one of the constants of this class
     */
    public int kind() {
        return kind;
    }
}
```

```java
package org.stuntgres.pq;

import java.nio.charset.StandardCharsets;

/** Text crossing the native boundary: always UTF-8 bytes with a length. */
final class PqText {
    private static final byte[] EMPTY = new byte[0];

    private PqText() {
    }

    /**
     * Encodes text for a library argument that is a C string.
     *
     * @param text the text, not null
     * @param what names the argument in the error message
     * @return the UTF-8 bytes; an unpaired surrogate becomes {@code '?'}
     * @throws PqException kind BAD_ARGUMENT when the text contains U+0000
     */
    static byte[] cstring(String text, String what) throws PqException {
        if (text.indexOf('\0') >= 0) {
            throw new PqException(PqException.BAD_ARGUMENT,
                what + " contains a NUL character");
        }
        return text.isEmpty() ? EMPTY : text.getBytes(StandardCharsets.UTF_8);
    }

    /**
     * Checks a byte value that the library reads as a C string.
     *
     * @param value the bytes, not null
     * @param what names the argument in the error message
     * @throws PqException kind BAD_ARGUMENT when a byte is zero
     */
    static void checkNoNul(byte[] value, String what) throws PqException {
        for (byte b : value) {
            if (b == 0) {
                throw new PqException(PqException.BAD_ARGUMENT,
                    what + " contains a NUL character");
            }
        }
    }

    /**
     * Decodes UTF-8 bytes; a malformed sequence becomes U+FFFD.
     *
     * @param bytes the buffer
     * @param offset first byte
     * @param length number of bytes
     * @return the text
     */
    static String decode(byte[] bytes, int offset, int length) {
        return length == 0 ? "" : new String(bytes, offset, length, StandardCharsets.UTF_8);
    }

    /**
     * Extracts one field from the packed form the shim produces for errors and
     * notices: for every field its code byte, its UTF-8 value and one zero byte.
     *
     * @param packed the packed fields
     * @param code a field code, one of the constants of {@code DiagField}
     * @return the value, or null when the field is absent
     */
    static String field(byte[] packed, int code) {
        int i = 0;
        while (i < packed.length) {
            int end = i + 1;
            while (end < packed.length && packed[end] != 0) {
                end++;
            }
            if (packed[i] == (byte) code) {
                return decode(packed, i + 1, end - i - 1);
            }
            i = end + 1;
        }
        return null;
    }

    /** Removes trailing line feeds, as library messages end with one. */
    static String trimNewlines(String text) {
        int end = text.length();
        while (end > 0 && text.charAt(end - 1) == '\n') {
            end--;
        }
        return text.substring(0, end);
    }
}
```

```java
package org.stuntgres.pq;

/** Builds a libpq connection string of {@code keyword='value'} pairs. */
public final class Conninfo {
    private final StringBuilder text = new StringBuilder();

    /** Creates an empty connection string. */
    public Conninfo() {
    }

    /**
     * Appends a keyword with its value, quoted so that any value is taken literally.
     *
     * @param keyword a libpq keyword, for example {@code "dbname"}
     * @param value the value; may be empty
     * @return this object
     */
    public Conninfo add(String keyword, String value) {
        if (text.length() > 0) {
            text.append(' ');
        }
        text.append(keyword).append("='");
        for (int i = 0; i < value.length(); i++) {
            char c = value.charAt(i);
            if (c == '\'' || c == '\\') {
                text.append('\\');
            }
            text.append(c);
        }
        text.append('\'');
        return this;
    }

    /**
     * Returns the connection string.
     *
     * @return for example {@code host='h' dbname='it\'s'}
     */
    @Override
    public String toString() {
        return text.toString();
    }
}
```

```java
package org.stuntgres.pq;

/** A notice or warning the server sent. Immutable. */
public final class PqNotice {
    private final byte[] fields;
    private final String message;

    /** Built from the packed fields and the formatted message the shim queued. */
    PqNotice(byte[] fields, byte[] message) {
        this.fields = fields;
        this.message = PqText.trimNewlines(PqText.decode(message, 0, message.length));
    }

    /**
     * Returns a field of the notice.
     *
     * @param code a constant of {@link DiagField}
     * @return the value, or null when the field is absent
     */
    public String field(int code) {
        return PqText.field(fields, code);
    }

    /**
     * Returns the notice as the library formats it.
     *
     * @return for example {@code "NOTICE:  hello"}
     */
    public String message() {
        return message;
    }

    @Override
    public String toString() {
        return message;
    }
}
```

```java
package org.stuntgres.pq;

/** A notification delivered by NOTIFY. Immutable. */
public final class PqNotification {
    private final int backendPid;
    private final String channel;
    private final String payload;

    /** Called by the shim (sjni_notify.c). */
    PqNotification(int backendPid, byte[] channel, byte[] payload) {
        this.backendPid = backendPid;
        this.channel = PqText.decode(channel, 0, channel.length);
        this.payload = PqText.decode(payload, 0, payload.length);
    }

    /**
     * Returns the process id of the notifying session.
     *
     * @return the backend pid
     */
    public int backendPid() {
        return backendPid;
    }

    /**
     * Returns the channel name.
     *
     * @return the name
     */
    public String channel() {
        return channel;
    }

    /**
     * Returns the payload.
     *
     * @return the payload; empty when none was given
     */
    public String payload() {
        return payload;
    }

    @Override
    public String toString() {
        return "PqNotification[" + backendPid + ", " + channel + ", " + payload + "]";
    }
}
```

### 9.4 `PqLibrary` (PQ-02C)

| Member | Behavior |
|---|---|
| `static void load(List<String> dependencies, String libraryPath, String symbolPrefix) throws PqException` (package-private) | Under the class's lock: encode the paths, call `load0`; on the first success read `features0()` and `libVersion0()` once and remember path and prefix. Called by `NativeLoader` after `System.load` of the shim. |
| `public static boolean isLoaded()` | True after a successful `load`. |
| `public static boolean hasFeature(int feature)` | Bit `feature` of the feature mask; false when nothing is loaded. |
| `public static int libVersion()` | What the library's `libVersion` returned (180006 for libpq 18.6 and for libspq); 0 when nothing is loaded. For diagnostics only. |
| `public static String path()`, `public static String prefix()` | Path and prefix of the loaded library, null when nothing is loaded. |
| `static void require(int feature) throws PqException` (package-private) | NOT_LOADED when nothing is loaded; UNSUPPORTED `the loaded library does not provide <prefix><Feature.name(feature)>` when the bit is clear. |

The complete file:

```java
package org.stuntgres.pq;

import java.nio.charset.StandardCharsets;
import java.util.List;

/**
 * The target library behind the shim: libspq, or a real libpq in tests. One library
 * is loaded per process, by {@link NativeLoader}; this class answers questions about it.
 */
public final class PqLibrary {
    private static final Object LOCK = new Object();
    private static volatile boolean loaded;
    private static int features;
    private static int libVersion;
    private static String path;
    private static String prefix;

    private PqLibrary() {
    }

    /**
     * Loads the target library through the shim; the shim itself must already be loaded
     * with {@code System.load}. A repeated call with the same path and prefix does nothing.
     *
     * @param dependencies absolute paths of libraries to open first, in order
     * @param libraryPath absolute path of the target library
     * @param symbolPrefix {@code "SPQ"} for libspq, {@code "PQ"} for a real libpq
     * @throws PqException kind LOAD_FAILED with the reason
     */
    static void load(List<String> dependencies, String libraryPath, String symbolPrefix)
            throws PqException {
        synchronized (LOCK) {
            byte[][] deps = new byte[dependencies.size()][];
            for (int i = 0; i < deps.length; i++) {
                deps[i] = PqText.cstring(dependencies.get(i), "a library path");
            }
            load0(deps, PqText.cstring(libraryPath, "the library path"),
                symbolPrefix.getBytes(StandardCharsets.US_ASCII));
            if (!loaded) {
                features = features0();
                libVersion = libVersion0();
                path = libraryPath;
                prefix = symbolPrefix;
                loaded = true;
            }
        }
    }

    /**
     * Tells whether the target library is loaded.
     *
     * @return true after a successful load
     */
    public static boolean isLoaded() {
        return loaded;
    }

    /**
     * Tells whether the loaded library has an optional function.
     *
     * @param feature a constant of {@link Feature}
     * @return true when the function exists; false when no library is loaded
     */
    public static boolean hasFeature(int feature) {
        return loaded && (features & (1 << feature)) != 0;
    }

    /**
     * Returns what the library's {@code libVersion} function reports (180006 for
     * libspq and for libpq 18.6). Never decide on features with it; use
     * {@link #hasFeature(int)}.
     *
     * @return the version number, 0 when no library is loaded
     */
    public static int libVersion() {
        return loaded ? libVersion : 0;
    }

    /**
     * Returns the absolute path of the loaded library.
     *
     * @return the path, or null when no library is loaded
     */
    public static String path() {
        return loaded ? path : null;
    }

    /**
     * Returns the symbol prefix of the loaded library.
     *
     * @return {@code "SPQ"} or {@code "PQ"}, or null when no library is loaded
     */
    public static String prefix() {
        return loaded ? prefix : null;
    }

    /**
     * Fails unless the loaded library has an optional function.
     *
     * @param feature a constant of {@link Feature}
     * @throws PqException kind NOT_LOADED or UNSUPPORTED
     */
    static void require(int feature) throws PqException {
        if (!loaded) {
            throw new PqException(PqException.NOT_LOADED, "the native library is not loaded");
        }
        if (!hasFeature(feature)) {
            throw new PqException(PqException.UNSUPPORTED,
                "the loaded library does not provide " + prefix + Feature.name(feature));
        }
    }

    private static native void load0(byte[][] dependencies, byte[] path, byte[] prefix)
        throws PqException;

    private static native int features0() throws PqException;

    private static native int libVersion0() throws PqException;
}
```

### 9.5 The connection: a chain of abstract classes

`PqConnection` is the public, final class. Its methods are declared in a chain of public
abstract classes with package-private constructors, one file and one C file per family,
so that parts that work in parallel never share a file:

```
ConnBase      connect, close, status, error message                 PQ-04A  sjni_conn.c
 └ ConnSession   transaction status, parameters, version, pid       PQ-04B  sjni_session.c
  └ ConnNotices   takeNotices                                       PQ-08   sjni_notice.c
   └ ConnExec      exec, sendQuery, getResult, execAll              PQ-06A  sjni_exec.c
    └ ConnParams    execParams                                      PQ-06B  sjni_params.c
     └ ConnPrepared  prepare, execPrepared, describe, close         PQ-07   sjni_prepared.c
      └ ConnNotify    notifies                                      PQ-10   sjni_notify.c
       └ ConnCopy      putCopyData, putCopyEnd, getCopyData         PQ-11   sjni_copy.c
        └ ConnEscape    escapeLiteral, escapeIdentifier, encoding   PQ-12   sjni_escape.c
         └ PqConnection  connect(conninfo), connect(conninfo, ms)   PQ-04B
```

PQ-04B creates `ConnSession`, `PqConnection` and the seven classes between them as stubs
(11.2: a class with only its constructor); each later part replaces its own stub with
the file below. The order of the chain is fixed by this document and has no meaning
beyond letting a class call the methods above it.

| Method | Behavior | Raises |
|---|---|---|
| `PqConnection.connect(String conninfo)`; `connect(String conninfo, long timeoutMillis)` | `ConnBase.open`: `NativeLoader.ensureLoaded()`; `connect0`; when `status0` is not OK read `errorMessage0`, `finish0` and raise CONNECT_FAILED with the trimmed message; else wrap the handle. A CALL_FAILED of `connect0` (`timeout expired`) is re-raised as CONNECT_FAILED. `timeoutMillis` 0 or negative: no limit. | CONNECT_FAILED, BAD_ARGUMENT, loader kinds |
| `close()` | Runs the release action (9.1): `finish0` once. Never raises. | |
| `isClosed()` | True after `close()`. | |
| `status()` | `ConnStatus` value. | CLOSED |
| `errorMessage()` | The connection's last message, trailing line feeds removed; empty when none. | CLOSED |
| `transactionStatus()` | `TransactionStatus` value. | CLOSED |
| `parameterStatus(String name)` | The reported value or null. | CLOSED, BAD_ARGUMENT |
| `serverVersion()`, `backendPid()` | The library's numbers. | CLOSED |
| `takeNotices()` | The queued notices, oldest first, in a new list; the immutable empty list when there are none. Each notice is returned once. | CLOSED |
| `exec(String sql)` | `ResultCopy.exec0`: the last result of the string, or the first error result. | CLOSED, BAD_ARGUMENT, TOO_LARGE, CALL_FAILED `the command failed: ...` |
| `sendQuery(String sql)` | `sendQuery0`; false → CALL_FAILED `sending the statement failed: <errorMessage()>`. | CLOSED, BAD_ARGUMENT, CALL_FAILED |
| `getResult()` | The next result or null. | CLOSED, TOO_LARGE |
| `execAll(String sql)` | `sendQuery`, then `getResult` until null, collecting; **stop rule:** after adding a result whose status is COPY_IN, COPY_OUT or COPY_BOTH return at once (the library would return that result again and again until the copy is done); the caller performs the copy and then calls `getResult()` for the rest. | as the two above |
| `execParams(String sql, int[] paramTypes, byte[][] paramValues, int[] paramFormats)` | `checkParams` (below), `execParams0`. `paramTypes` null: the server decides every type; `paramFormats` null: all text. | CLOSED, BAD_ARGUMENT, TOO_LARGE, CALL_FAILED |
| `ConnParams.checkParams(int[] types, byte[][] values, int[] formats)` (static, package-private) | More than 65535 values; `types` or `formats` not null and of another length than `values`; a format other than 0 or 1; a zero byte in a text-format value: each BAD_ARGUMENT with the texts of 9.3. A null value is SQL NULL, an empty array the empty string. | BAD_ARGUMENT |
| `prepare(String name, String sql, int[] paramTypes)` | `prepare0`; more than 65535 types → BAD_ARGUMENT. | as `exec` |
| `execPrepared(String name, byte[][] paramValues, int[] paramFormats)` | `checkParams(null, values, formats)`, `execPrepared0`. | as `execParams` |
| `describePrepared(String name)` | `describePrepared0`. | as `exec` |
| `closePrepared(String name)` | `PqLibrary.require(Feature.CLOSE_PREPARED)`, `closePrepared0`. | UNSUPPORTED, and as `exec` |
| `notifies(long timeoutMillis)` | `notifies0` as a list (7.4). | CLOSED, CALL_FAILED |
| `putCopyData(byte[] buffer, int offset, int length)` | `Objects.checkFromIndexSize`; nothing is sent for length 0; a value other than 1 from `putCopyData0` → CALL_FAILED `sending COPY data failed: <errorMessage()>`. | CLOSED, CALL_FAILED |
| `putCopyEnd(String errorMessage)` | Other than 1 → CALL_FAILED `ending COPY failed: <errorMessage()>`. | CLOSED, BAD_ARGUMENT, CALL_FAILED |
| `getCopyData()` | One row or null at the end (7.5). | CLOSED, CALL_FAILED `reading COPY data failed: ...` |
| `escapeLiteral(String text)`, `escapeIdentifier(String text)` | The quoted form as the library returns it (measured values below). | CLOSED, BAD_ARGUMENT, CALL_FAILED `escaping a literal failed: ...` (`escaping an identifier failed: ...`) |
| `clientEncoding()` | `parameterStatus("client_encoding")`. | CLOSED |
| `setClientEncoding(String name)` | Nonzero from the library → CALL_FAILED `setting the client encoding failed: <errorMessage()>`. | CLOSED, BAD_ARGUMENT, CALL_FAILED |

Measured values of the two escape methods (oracle and server mode agree). In Java
source notation: `escapeLiteral("it's a \\ test")` returns `" E'it''s a \\\\ test'"`
(a leading space, then `E`, the quote doubled, the backslash doubled);
`escapeLiteral("")` returns `"''"`; `escapeLiteral("\uD83D\uDE00")` returns the emoji
between single quotes; `escapeIdentifier("a\"b")` returns `"\"a\"\"b\""`.

The complete files, in chain order:

```java
package org.stuntgres.pq;

import java.lang.ref.Reference;
import java.util.concurrent.atomic.AtomicLong;

/**
 * A connection handle with its life cycle and status accessors. One thread at a time
 * may use a connection; the methods take no lock.
 */
public abstract class ConnBase implements AutoCloseable {
    /** Owns the native handle; shared with the release action, never with the owner. */
    private static final class State implements Runnable {
        private final AtomicLong handle;

        State(long handle) {
            this.handle = new AtomicLong(handle);
        }

        @Override
        public void run() {
            long h = handle.getAndSet(0);
            if (h != 0) {
                finish0(h);
            }
        }
    }

    private final State state;
    private final Runnable release;

    ConnBase(long handle) {
        this.state = new State(handle);
        this.release = HandleRelease.register(this, state);
    }

    /**
     * Opens a native connection and returns its handle.
     *
     * @param conninfo the libpq connection string
     * @param timeoutMillis the longest time the attempt may take; 0 for no limit
     * @return the handle of a connection with status OK
     * @throws PqException kind CONNECT_FAILED with the library's message (or
     *         {@code "timeout expired"}), or a load failure
     */
    static long open(String conninfo, long timeoutMillis) throws PqException {
        NativeLoader.ensureLoaded();
        long handle;
        try {
            handle = connect0(PqText.cstring(conninfo, "the connection string"),
                Math.max(timeoutMillis, 0));
        } catch (PqException e) {
            if (e.kind() == PqException.CALL_FAILED) {
                throw new PqException(PqException.CONNECT_FAILED, e.getMessage());
            }
            throw e;
        }
        boolean ok = false;
        try {
            if (status0(handle) != ConnStatus.OK) {
                byte[] message = errorMessage0(handle);
                throw new PqException(PqException.CONNECT_FAILED,
                    PqText.trimNewlines(PqText.decode(message, 0, message.length)));
            }
            ok = true;
            return handle;
        } finally {
            if (!ok) {
                finish0(handle);
            }
        }
    }

    /**
     * Returns the native handle for a call.
     *
     * @return the handle, never 0
     * @throws PqException kind CLOSED after {@link #close()}
     */
    final long handle() throws PqException {
        long h = state.handle.get();
        if (h == 0) {
            throw new PqException(PqException.CLOSED, "the connection is closed");
        }
        return h;
    }

    /**
     * Tells whether {@link #close()} was called.
     *
     * @return true when closed
     */
    public final boolean isClosed() {
        return state.handle.get() == 0;
    }

    /** Ends the session and frees the native connection. Does nothing when already closed. */
    @Override
    public final void close() {
        release.run();
    }

    /**
     * Returns the connection status.
     *
     * @return a constant of {@link ConnStatus}
     * @throws PqException kind CLOSED
     */
    public final int status() throws PqException {
        try {
            return status0(handle());
        } finally {
            Reference.reachabilityFence(this);
        }
    }

    /**
     * Returns the message of the most recent failed operation on this connection.
     *
     * @return the message without trailing line feeds; empty when there is none
     * @throws PqException kind CLOSED
     */
    public final String errorMessage() throws PqException {
        try {
            byte[] message = errorMessage0(handle());
            return PqText.trimNewlines(PqText.decode(message, 0, message.length));
        } finally {
            Reference.reachabilityFence(this);
        }
    }

    private static native long connect0(byte[] conninfo, long timeoutMillis)
        throws PqException;

    private static native void finish0(long handle);

    private static native int status0(long handle) throws PqException;

    private static native byte[] errorMessage0(long handle) throws PqException;
}
```

```java
package org.stuntgres.pq;

import java.lang.ref.Reference;

/** Facts about the session behind a connection. */
public abstract class ConnSession extends ConnBase {
    ConnSession(long handle) {
        super(handle);
    }

    /**
     * Returns the transaction status.
     *
     * @return a constant of {@link TransactionStatus}
     * @throws PqException kind CLOSED
     */
    public final int transactionStatus() throws PqException {
        try {
            return transactionStatus0(handle());
        } finally {
            Reference.reachabilityFence(this);
        }
    }

    /**
     * Returns a parameter the server reports.
     *
     * @param name the parameter name, for example {@code "server_version"}
     * @return the value, or null when the server does not report that parameter
     * @throws PqException kind CLOSED or BAD_ARGUMENT
     */
    public final String parameterStatus(String name) throws PqException {
        try {
            byte[] value = parameterStatus0(handle(), PqText.cstring(name, "the parameter name"));
            return value == null ? null : PqText.decode(value, 0, value.length);
        } finally {
            Reference.reachabilityFence(this);
        }
    }

    /**
     * Returns the server version number.
     *
     * @return for example 180006
     * @throws PqException kind CLOSED
     */
    public final int serverVersion() throws PqException {
        try {
            return serverVersion0(handle());
        } finally {
            Reference.reachabilityFence(this);
        }
    }

    /**
     * Returns the backend process id of the session.
     *
     * @return the id
     * @throws PqException kind CLOSED
     */
    public final int backendPid() throws PqException {
        try {
            return backendPid0(handle());
        } finally {
            Reference.reachabilityFence(this);
        }
    }

    private static native int transactionStatus0(long handle) throws PqException;

    private static native byte[] parameterStatus0(long handle, byte[] name) throws PqException;

    private static native int serverVersion0(long handle) throws PqException;

    private static native int backendPid0(long handle) throws PqException;
}
```

```java
package org.stuntgres.pq;

import java.lang.ref.Reference;
import java.util.ArrayList;
import java.util.Collections;
import java.util.List;

/** Notices the server sent while this connection ran commands. */
public abstract class ConnNotices extends ConnSession {
    ConnNotices(long handle) {
        super(handle);
    }

    /**
     * Removes and returns the notices received since the last call, oldest first.
     * Call it after every command that can produce notices.
     *
     * @return the notices; an empty list when there are none
     * @throws PqException kind CLOSED
     */
    public final List<PqNotice> takeNotices() throws PqException {
        try {
            byte[][] raw = drainNotices0(handle());
            if (raw == null) {
                return Collections.emptyList();
            }
            List<PqNotice> notices = new ArrayList<>(raw.length / 2);
            for (int i = 0; i < raw.length; i += 2) {
                notices.add(new PqNotice(raw[i], raw[i + 1]));
            }
            return notices;
        } finally {
            Reference.reachabilityFence(this);
        }
    }

    private static native byte[][] drainNotices0(long handle) throws PqException;
}
```

```java
package org.stuntgres.pq;

import java.lang.ref.Reference;
import java.util.ArrayList;
import java.util.List;

/** Simple and parameterized execution. */
public abstract class ConnExec extends ConnNotices {
    ConnExec(long handle) {
        super(handle);
    }

    /**
     * Runs a statement string and returns its last result, as {@code PQexec} does.
     *
     * @param sql one or more statements
     * @return the last result, or the first error result
     * @throws PqException kind CLOSED, BAD_ARGUMENT, TOO_LARGE or CALL_FAILED
     */
    public final PqResult exec(String sql) throws PqException {
        try {
            return ResultCopy.exec0(handle(), PqText.cstring(sql, "the statement"));
        } finally {
            Reference.reachabilityFence(this);
        }
    }

    /**
     * Sends a statement string without waiting for results; collect them with
     * {@link #getResult()} until it returns null.
     *
     * @param sql one or more statements
     * @throws PqException kind CALL_FAILED when the library refuses the command
     */
    public final void sendQuery(String sql) throws PqException {
        try {
            long h = handle();
            if (!sendQuery0(h, PqText.cstring(sql, "the statement"))) {
                throw new PqException(PqException.CALL_FAILED,
                    "sending the statement failed: " + errorMessage());
            }
        } finally {
            Reference.reachabilityFence(this);
        }
    }

    /**
     * Waits for the next result of the command in progress.
     *
     * @return the result, or null when the command is complete
     * @throws PqException kind CLOSED or TOO_LARGE
     */
    public final PqResult getResult() throws PqException {
        try {
            return getResult0(handle());
        } finally {
            Reference.reachabilityFence(this);
        }
    }

    /**
     * Runs a statement string and returns every result, one per statement. Collection
     * stops after a result whose status is COPY_IN, COPY_OUT or COPY_BOTH: the caller
     * then performs the copy and calls {@link #getResult()} for the remaining results.
     *
     * @param sql one or more statements
     * @return the results in order; never empty for a non-empty statement string
     * @throws PqException as {@link #sendQuery(String)} and {@link #getResult()}
     */
    public final List<PqResult> execAll(String sql) throws PqException {
        sendQuery(sql);
        List<PqResult> results = new ArrayList<>();
        for (;;) {
            PqResult result = getResult();
            if (result == null) {
                return results;
            }
            results.add(result);
            int status = result.status();
            if (status == ExecStatus.COPY_IN || status == ExecStatus.COPY_OUT
                    || status == ExecStatus.COPY_BOTH) {
                return results;
            }
        }
    }

    private static native boolean sendQuery0(long handle, byte[] sql) throws PqException;

    private static native PqResult getResult0(long handle) throws PqException;
}
```

```java
package org.stuntgres.pq;

import java.lang.ref.Reference;

/** Execution of one statement with parameters. */
public abstract class ConnParams extends ConnExec {
    ConnParams(long handle) {
        super(handle);
    }

    /**
     * Runs one statement with parameters. Results arrive in text format.
     *
     * @param sql one statement with {@code $1}, {@code $2}, ... placeholders
     * @param paramTypes the type OID of each parameter (0 lets the server decide), or
     *        null for all zero
     * @param paramValues the bytes of each parameter; a null element is SQL NULL
     * @param paramFormats the format of each parameter (0 text, 1 binary), or null for
     *        all text
     * @return the result
     * @throws PqException kind BAD_ARGUMENT when the arrays differ in length, there are
     *         more than 65535 parameters, a format is neither 0 nor 1 or a text-format
     *         value contains a zero byte
     */
    public final PqResult execParams(String sql, int[] paramTypes, byte[][] paramValues,
            int[] paramFormats) throws PqException {
        try {
            checkParams(paramTypes, paramValues, paramFormats);
            return execParams0(handle(), PqText.cstring(sql, "the statement"), paramTypes,
                paramValues, paramFormats);
        } finally {
            Reference.reachabilityFence(this);
        }
    }

    /** Validates parameter arrays before they cross the boundary. */
    static void checkParams(int[] paramTypes, byte[][] paramValues, int[] paramFormats)
            throws PqException {
        if (paramValues.length > 65535) {
            throw new PqException(PqException.BAD_ARGUMENT,
                "too many parameters: " + paramValues.length + " (the limit is 65535)");
        }
        if (paramTypes != null && paramTypes.length != paramValues.length) {
            throw new PqException(PqException.BAD_ARGUMENT,
                "got " + paramTypes.length + " parameter types for " + paramValues.length
                    + " values");
        }
        if (paramFormats != null && paramFormats.length != paramValues.length) {
            throw new PqException(PqException.BAD_ARGUMENT,
                "got " + paramFormats.length + " parameter formats for " + paramValues.length
                    + " values");
        }
        for (int i = 0; i < paramValues.length; i++) {
            int format = paramFormats == null ? 0 : paramFormats[i];
            if (format != 0 && format != 1) {
                throw new PqException(PqException.BAD_ARGUMENT,
                    "parameter $" + (i + 1) + " has format " + format + " (0 or 1 expected)");
            }
            if (format == 0 && paramValues[i] != null) {
                PqText.checkNoNul(paramValues[i], "parameter $" + (i + 1));
            }
        }
    }

    private static native PqResult execParams0(long handle, byte[] sql, int[] paramTypes,
        byte[][] paramValues, int[] paramFormats) throws PqException;
}
```

```java
package org.stuntgres.pq;

import java.lang.ref.Reference;

/** Prepared statements of the session. */
public abstract class ConnPrepared extends ConnParams {
    ConnPrepared(long handle) {
        super(handle);
    }

    /**
     * Prepares a statement.
     *
     * @param name the statement name; empty for the unnamed statement
     * @param sql one statement
     * @param paramTypes parameter type OIDs, or null to let the server decide all
     * @return a COMMAND_OK result or an error result
     * @throws PqException kind CLOSED, BAD_ARGUMENT or CALL_FAILED
     */
    public final PqResult prepare(String name, String sql, int[] paramTypes) throws PqException {
        try {
            if (paramTypes != null && paramTypes.length > 65535) {
                throw new PqException(PqException.BAD_ARGUMENT,
                    "too many parameters: " + paramTypes.length + " (the limit is 65535)");
            }
            return prepare0(handle(), PqText.cstring(name, "the statement name"),
                PqText.cstring(sql, "the statement"), paramTypes);
        } finally {
            Reference.reachabilityFence(this);
        }
    }

    /**
     * Executes a prepared statement. Results arrive in text format.
     *
     * @param name the statement name
     * @param paramValues the bytes of each parameter; a null element is SQL NULL
     * @param paramFormats the format of each parameter (0 text, 1 binary), or null for
     *        all text
     * @return the result
     * @throws PqException kind CLOSED, BAD_ARGUMENT, TOO_LARGE or CALL_FAILED
     */
    public final PqResult execPrepared(String name, byte[][] paramValues, int[] paramFormats)
            throws PqException {
        try {
            ConnParams.checkParams(null, paramValues, paramFormats);
            return execPrepared0(handle(), PqText.cstring(name, "the statement name"),
                paramValues, paramFormats);
        } finally {
            Reference.reachabilityFence(this);
        }
    }

    /**
     * Describes a prepared statement: parameter types and result columns, no rows.
     *
     * @param name the statement name
     * @return a COMMAND_OK result with field and parameter descriptions, or an error result
     * @throws PqException kind CLOSED, BAD_ARGUMENT or CALL_FAILED
     */
    public final PqResult describePrepared(String name) throws PqException {
        try {
            return describePrepared0(handle(), PqText.cstring(name, "the statement name"));
        } finally {
            Reference.reachabilityFence(this);
        }
    }

    /**
     * Closes a prepared statement.
     *
     * @param name the statement name
     * @return a COMMAND_OK result (also for an unknown name) or an error result
     * @throws PqException kind UNSUPPORTED when the library lacks closePrepared
     */
    public final PqResult closePrepared(String name) throws PqException {
        try {
            PqLibrary.require(Feature.CLOSE_PREPARED);
            return closePrepared0(handle(), PqText.cstring(name, "the statement name"));
        } finally {
            Reference.reachabilityFence(this);
        }
    }

    private static native PqResult prepare0(long handle, byte[] name, byte[] sql,
        int[] paramTypes) throws PqException;

    private static native PqResult execPrepared0(long handle, byte[] name, byte[][] paramValues,
        int[] paramFormats) throws PqException;

    private static native PqResult describePrepared0(long handle, byte[] name)
        throws PqException;

    private static native PqResult closePrepared0(long handle, byte[] name) throws PqException;
}
```

```java
package org.stuntgres.pq;

import java.lang.ref.Reference;
import java.util.Arrays;
import java.util.Collections;
import java.util.List;

/** Notifications of LISTEN and NOTIFY. */
public abstract class ConnNotify extends ConnPrepared {
    ConnNotify(long handle) {
        super(handle);
    }

    /**
     * Returns the notifications that have arrived, waiting for the first one when
     * none is pending.
     *
     * @param timeoutMillis 0 to return at once, a positive number to wait at most that
     *        long, a negative number to wait without limit
     * @return the notifications in arrival order; empty when the time ran out
     * @throws PqException kind CLOSED or CALL_FAILED (the connection is lost)
     */
    public final List<PqNotification> notifies(long timeoutMillis) throws PqException {
        try {
            PqNotification[] found = notifies0(handle(), timeoutMillis);
            return found.length == 0 ? Collections.emptyList() : Arrays.asList(found);
        } finally {
            Reference.reachabilityFence(this);
        }
    }

    private static native PqNotification[] notifies0(long handle, long timeoutMillis)
        throws PqException;
}
```

```java
package org.stuntgres.pq;

import java.lang.ref.Reference;
import java.util.Objects;

/** The COPY sub-protocol. */
public abstract class ConnCopy extends ConnNotify {
    ConnCopy(long handle) {
        super(handle);
    }

    /**
     * Sends data of a COPY FROM STDIN in progress.
     *
     * @param buffer the data
     * @param offset first byte
     * @param length number of bytes
     * @throws PqException kind CALL_FAILED when the library reports failure
     */
    public final void putCopyData(byte[] buffer, int offset, int length) throws PqException {
        try {
            Objects.checkFromIndexSize(offset, length, buffer.length);
            long h = handle();
            if (length > 0 && putCopyData0(h, buffer, offset, length) != 1) {
                throw new PqException(PqException.CALL_FAILED,
                    "sending COPY data failed: " + errorMessage());
            }
        } finally {
            Reference.reachabilityFence(this);
        }
    }

    /**
     * Ends a COPY FROM STDIN; afterwards collect the result with {@code getResult()}.
     *
     * @param errorMessage null to complete the copy, or a text that makes it fail
     * @throws PqException kind CALL_FAILED when the library reports failure
     */
    public final void putCopyEnd(String errorMessage) throws PqException {
        try {
            byte[] error = errorMessage == null ? null
                : PqText.cstring(errorMessage, "the error message");
            if (putCopyEnd0(handle(), error) != 1) {
                throw new PqException(PqException.CALL_FAILED,
                    "ending COPY failed: " + errorMessage());
            }
        } finally {
            Reference.reachabilityFence(this);
        }
    }

    /**
     * Waits for the next row of a COPY TO STDOUT in progress.
     *
     * @return the row, or null when the copy is done (then call {@code getResult()})
     * @throws PqException kind CALL_FAILED when the library reports an error
     */
    public final byte[] getCopyData() throws PqException {
        try {
            return getCopyData0(handle());
        } finally {
            Reference.reachabilityFence(this);
        }
    }

    private static native int putCopyData0(long handle, byte[] buffer, int offset, int length)
        throws PqException;

    private static native int putCopyEnd0(long handle, byte[] errorMessage) throws PqException;

    private static native byte[] getCopyData0(long handle) throws PqException;
}
```

```java
package org.stuntgres.pq;

import java.lang.ref.Reference;

/** Escaping and the client encoding. */
public abstract class ConnEscape extends ConnCopy {
    ConnEscape(long handle) {
        super(handle);
    }

    /**
     * Quotes text as an SQL string literal, with the surrounding quotes.
     *
     * @param text the text
     * @return the literal, for example {@code 'it''s'}
     * @throws PqException kind BAD_ARGUMENT for a NUL character, CALL_FAILED otherwise
     */
    public final String escapeLiteral(String text) throws PqException {
        try {
            byte[] out = escapeLiteral0(handle(), PqText.cstring(text, "the text"));
            return PqText.decode(out, 0, out.length);
        } finally {
            Reference.reachabilityFence(this);
        }
    }

    /**
     * Quotes text as an SQL identifier, with the surrounding double quotes.
     *
     * @param text the identifier
     * @return the quoted identifier, for example {@code "a""b"}
     * @throws PqException kind BAD_ARGUMENT for a NUL character, CALL_FAILED otherwise
     */
    public final String escapeIdentifier(String text) throws PqException {
        try {
            byte[] out = escapeIdentifier0(handle(), PqText.cstring(text, "the identifier"));
            return PqText.decode(out, 0, out.length);
        } finally {
            Reference.reachabilityFence(this);
        }
    }

    /**
     * Returns the client encoding the server reports.
     *
     * @return for example {@code "UTF8"}
     * @throws PqException kind CLOSED
     */
    public final String clientEncoding() throws PqException {
        return parameterStatus("client_encoding");
    }

    /**
     * Changes the client encoding of the session.
     *
     * @param name an encoding name, for example {@code "UTF8"}
     * @throws PqException kind CALL_FAILED when the server rejects the name
     */
    public final void setClientEncoding(String name) throws PqException {
        try {
            if (setClientEncoding0(handle(), PqText.cstring(name, "the encoding name")) != 0) {
                throw new PqException(PqException.CALL_FAILED,
                    "setting the client encoding failed: " + errorMessage());
            }
        } finally {
            Reference.reachabilityFence(this);
        }
    }

    private static native byte[] escapeLiteral0(long handle, byte[] text) throws PqException;

    private static native byte[] escapeIdentifier0(long handle, byte[] text) throws PqException;

    private static native int setClientEncoding0(long handle, byte[] name) throws PqException;
}
```

```java
package org.stuntgres.pq;

/**
 * One session of the target library. Its methods come from the chain of base classes
 * {@link ConnBase}, {@link ConnNotices}, {@link ConnExec}, {@link ConnPrepared},
 * {@link ConnNotify}, {@link ConnCopy} and {@link ConnEscape}.
 */
public final class PqConnection extends ConnEscape {
    private PqConnection(long handle) {
        super(handle);
    }

    /**
     * Opens a session. The native libraries are loaded by the first call.
     *
     * @param conninfo a libpq connection string
     * @return a connection with status OK
     * @throws PqException kind CONNECT_FAILED with the library's message when the
     *         attempt fails; the kinds of {@link NativeLoader#ensureLoaded()} otherwise
     */
    public static PqConnection connect(String conninfo) throws PqException {
        return new PqConnection(ConnBase.open(conninfo, 0));
    }

    /**
     * Opens a session, giving up after a time limit.
     *
     * @param conninfo a libpq connection string
     * @param timeoutMillis the longest time the attempt may take; 0 for no limit
     * @return a connection with status OK
     * @throws PqException as {@link #connect(String)}; kind CONNECT_FAILED with the
     *         message {@code "timeout expired"} when the limit is reached
     */
    public static PqConnection connect(String conninfo, long timeoutMillis) throws PqException {
        return new PqConnection(ConnBase.open(conninfo, timeoutMillis));
    }
}
```

### 9.6 `HandleRelease` and `PqCancel` (PQ-13, PQ-09)

`HandleRelease.register(Object owner, Runnable action)` returns a `Runnable` that runs
`action` unless it already ran. The stub of PQ-04A returns `action` itself (11.2). The
final class registers the pair with one shared `java.lang.ref.Cleaner`: when `owner`
becomes unreachable without `close()`, the cleaner's thread runs `action`. `action` is
the state object, which holds only the `AtomicLong`; it must not refer to the owner, or
the owner would never be collected.

Why the cleaner cannot interfere with a call: it runs only when the owner is not
reachable, and the fence of 9.1 keeps the owner reachable during every native call. Why
it cannot free twice: the action swaps the handle to 0 atomically. **(measured: a
connection dropped without `close()` was finished after `System.gc()` within 10 to 22
ms, its backend gone from `pg_stat_activity`; `close()` twice is harmless.)**

A JVM that ends with open connections needs no cleanup **(measured: return from `main`
and `System.exit(0)` with three open connections, an open transaction, a cancel handle
and a daemon thread blocked in a statement: exit code 0, the shutdown hook ran, the
idle sessions were gone on the server and the transaction was rolled back)**.
PostgreSQL keeps a session that is executing `pg_sleep` until the statement ends, the
stuntgres server ends it at once; no test asserts either.

```java
package org.stuntgres.pq;

import java.lang.ref.Cleaner;

/** Runs the release action of a native handle once: on close, or when its owner is collected. */
final class HandleRelease {
    private static final Cleaner CLEANER = Cleaner.create();

    private HandleRelease() {
    }

    /**
     * Registers a release action.
     *
     * @param owner the object whose collection triggers the action
     * @param action releases the handle; must not refer to {@code owner}
     * @return a runnable that runs {@code action} now unless it already ran
     */
    static Runnable register(Object owner, Runnable action) {
        Cleaner.Cleanable cleanable = CLEANER.register(owner, action);
        return cleanable::clean;
    }
}
```

`PqCancel`:

| Member | Behavior | Raises |
|---|---|---|
| `public static PqCancel of(ConnBase connection)` | `getCancel0(connection.handle())`, fence on the connection. A call on the connection like any other: not while another thread uses it. | CLOSED, CALL_FAILED |
| `public synchronized void cancel()` | Handle 0 → CLOSED `the cancel handle is closed`; `cancel0`; a non-null answer → CALL_FAILED `the cancel request failed: <text>`. May be called from any thread at any time, also after the connection was closed. | CLOSED, CALL_FAILED |
| `public synchronized void close()` | Runs the release action. | |

`cancel()` and `close()` synchronize on the cancel object, never on the connection, so a
cancel cannot be delayed by the call it is meant to interrupt.

```java
package org.stuntgres.pq;

import java.lang.ref.Reference;
import java.util.concurrent.atomic.AtomicLong;

/**
 * A cancel handle of one connection. {@link #cancel()} may be called from any thread
 * while another thread uses the connection, and after the connection was closed.
 */
public final class PqCancel implements AutoCloseable {
    private static final class State implements Runnable {
        private final AtomicLong handle;

        State(long handle) {
            this.handle = new AtomicLong(handle);
        }

        @Override
        public void run() {
            long h = handle.getAndSet(0);
            if (h != 0) {
                freeCancel0(h);
            }
        }
    }

    private final State state;
    private final Runnable release;

    private PqCancel(long handle) {
        this.state = new State(handle);
        this.release = HandleRelease.register(this, state);
    }

    /**
     * Creates the cancel handle of a connection. Like every other call on a connection
     * it must not run while another thread uses that connection.
     *
     * @param connection an open connection
     * @return the handle
     * @throws PqException kind CLOSED or CALL_FAILED
     */
    public static PqCancel of(ConnBase connection) throws PqException {
        try {
            return new PqCancel(getCancel0(connection.handle()));
        } finally {
            Reference.reachabilityFence(connection);
        }
    }

    /**
     * Asks the server to cancel the command the connection is running. Nothing happens
     * when no command is running.
     *
     * @throws PqException kind CLOSED after {@link #close()}; kind CALL_FAILED when the
     *         request could not be sent
     */
    public synchronized void cancel() throws PqException {
        long h = state.handle.get();
        if (h == 0) {
            throw new PqException(PqException.CLOSED, "the cancel handle is closed");
        }
        byte[] error = cancel0(h);
        if (error != null) {
            throw new PqException(PqException.CALL_FAILED, "the cancel request failed: "
                + PqText.trimNewlines(PqText.decode(error, 0, error.length)));
        }
    }

    /** Frees the handle. Does nothing when already closed. */
    @Override
    public synchronized void close() {
        release.run();
    }

    private static native long getCancel0(long connection) throws PqException;

    private static native byte[] cancel0(long cancel);

    private static native void freeCancel0(long cancel);
}
```

### 9.7 `PqCluster` and `PqExt` (PQ-14A, PQ-14B)

Both exist only for libspq; with a real libpq every method raises UNSUPPORTED.

| Method | Behavior | Raises |
|---|---|---|
| `PqCluster.version()` | `ensureLoaded`, `require(EXT_VERSION)`, `version0`. | UNSUPPORTED, loader kinds |
| `PqCluster.drop(String host, String port)` | `ensureLoaded`, `require(EXT_CLUSTER_DROP)`, `clusterDrop0`; an answer → CALL_FAILED `dropping the cluster failed: <text>`. | UNSUPPORTED, BAD_ARGUMENT, CALL_FAILED |
| `PqExt.reset(ConnBase connection)` | `require(EXT_RESET)`; false → CALL_FAILED `resetting the database failed: <errorMessage()>`. | UNSUPPORTED, CLOSED, CALL_FAILED |
| `PqExt.dump(ConnBase connection)` | `require(EXT_DUMP)`; null → CALL_FAILED `dumping the database failed: <errorMessage()>`. | UNSUPPORTED, CLOSED, CALL_FAILED, TOO_LARGE |
| `PqExt.restore(ConnBase connection, byte[] dump)` | `require(EXT_RESTORE)`; false → CALL_FAILED `restoring the database failed: <errorMessage()>`. | UNSUPPORTED, CLOSED, CALL_FAILED |

```java
package org.stuntgres.pq;

/** The libspq extensions that need no connection; a real libpq has none of them. */
public final class PqCluster {
    private PqCluster() {
    }

    /**
     * Returns the version of libspq.
     *
     * @return for example {@code "0.1.0"}
     * @throws PqException kind UNSUPPORTED when the loaded library is not libspq
     */
    public static String version() throws PqException {
        NativeLoader.ensureLoaded();
        PqLibrary.require(Feature.EXT_VERSION);
        byte[] text = version0();
        return PqText.decode(text, 0, text.length);
    }

    /**
     * Forgets a cluster with everything in it.
     *
     * @param host the cluster name
     * @param port the port part of the cluster name
     * @throws PqException kind CALL_FAILED with the library's message, for example
     *         while the cluster has open connections
     */
    public static void drop(String host, String port) throws PqException {
        NativeLoader.ensureLoaded();
        PqLibrary.require(Feature.EXT_CLUSTER_DROP);
        byte[] error = clusterDrop0(PqText.cstring(host, "the host"),
            PqText.cstring(port, "the port"));
        if (error != null) {
            throw new PqException(PqException.CALL_FAILED,
                "dropping the cluster failed: " + PqText.decode(error, 0, error.length));
        }
    }

    private static native byte[] version0() throws PqException;

    private static native byte[] clusterDrop0(byte[] host, byte[] port) throws PqException;
}
```

`PqExt.java` (**not run**: it compiles; its methods need libspq):

```java
package org.stuntgres.pq;

import java.lang.ref.Reference;

/** The libspq extensions that work on a connected database; a real libpq has none of them. */
public final class PqExt {
    private PqExt() {
    }

    /**
     * Removes all persistent objects and data of the connected database.
     *
     * @param connection an open connection
     * @throws PqException kind CALL_FAILED with the connection's message
     */
    public static void reset(ConnBase connection) throws PqException {
        try {
            PqLibrary.require(Feature.EXT_RESET);
            if (!reset0(connection.handle())) {
                throw new PqException(PqException.CALL_FAILED,
                    "resetting the database failed: " + connection.errorMessage());
            }
        } finally {
            Reference.reachabilityFence(connection);
        }
    }

    /**
     * Serializes the connected database.
     *
     * @param connection an open connection
     * @return the dump
     * @throws PqException kind CALL_FAILED with the connection's message
     */
    public static byte[] dump(ConnBase connection) throws PqException {
        try {
            PqLibrary.require(Feature.EXT_DUMP);
            byte[] dump = dump0(connection.handle());
            if (dump == null) {
                throw new PqException(PqException.CALL_FAILED,
                    "dumping the database failed: " + connection.errorMessage());
            }
            return dump;
        } finally {
            Reference.reachabilityFence(connection);
        }
    }

    /**
     * Replaces the connected database with the content of a dump.
     *
     * @param connection an open connection
     * @param dump a dump made by {@link #dump(ConnBase)}
     * @throws PqException kind CALL_FAILED with the connection's message
     */
    public static void restore(ConnBase connection, byte[] dump) throws PqException {
        try {
            PqLibrary.require(Feature.EXT_RESTORE);
            if (!restore0(connection.handle(), dump)) {
                throw new PqException(PqException.CALL_FAILED,
                    "restoring the database failed: " + connection.errorMessage());
            }
        } finally {
            Reference.reachabilityFence(connection);
        }
    }

    private static native boolean reset0(long handle) throws PqException;

    private static native byte[] dump0(long handle) throws PqException;

    private static native boolean restore0(long handle, byte[] dump) throws PqException;
}
```


## 10. Test harness

The suite is JUnit 5 (Jupiter 5.12.2) run by Surefire. It issues SQL through a public
API in a directory other than `reference/`, which `docs/TESTING.md` ("Placement policy")
does not allow today, exactly as for the libspq C++ tests and the Python binding;
recording the binding suites as sanctioned is the job of the documentation part that
JDBC-DESIGN-FEATURES creates (plan B15). The tests check the behavior of the binding;
SQL features stay with the reference tests.

### 10.1 Command line, output, exit codes

```sh
make -C bindings/jdbc check MODE=<unit|reference|oracle|server|native> [TEST=<selector>]
```

`MODE` defaults to `unit`. `TEST` is a Surefire selector: a class (`PqResultTest`), a
method (`PqResultTest#nullAgainstEmpty`), a pattern (`Pq*Test`), a comma-separated
list. The JDK is that of `JAVA_HOME` (3.2).

The Makefile builds the shim and the stand-in libraries and then runs
`tools/run_mode.sh <MODE> <TEST> -- mvn ... test`. The script

1. removes every environment variable whose name starts with `PG` or `STUNTGRES_`, so
   that a real libpq and the loader read nothing from the caller's environment;
2. for a mode other than `unit`, sources `tools/mode_<MODE>.sh` and calls its
   `mode_start`, which starts what the mode needs and appends `-Dname=value` words to
   the array `PROPS`; a missing mode file or a failed start ends the run with exit
   status 3 and one line `run_mode: cannot start <mode> mode: <reason>`;
3. runs the command with `-Dstuntgres.test.mode=<MODE>`,
   `-Dstuntgres.test.reporoot=<repository root>`, `-Dtest=<TEST>` when given, and the
   mode's properties; Maven hands them to the test JVM as system properties;
4. prints one summary line computed from `target/surefire-reports/TEST-*.xml`:
   `stuntgres-jdbc: mode=<mode> tests=<n> failures=<n> errors=<n> skipped=<n>`;
5. calls `mode_stop` on every way out: after a pass, a failure, Ctrl-C and SIGTERM (an
   `EXIT` trap and an `INT`/`TERM` trap). The command runs in the foreground, so a
   signal sent to the process group (Ctrl-C, `timeout`) reaches Maven and the test JVM
   directly, and the shell runs its trap as soon as they have ended.

| Exit status of `run_mode.sh` | Meaning |
|---|---|
| 0 | Every selected test passed (skips allowed). Without `TEST`, zero selected tests is a pass: a mode may have no test yet. |
| 1 | Maven failed: a compile error, a failed or erroneous test, a `TEST` pattern that matches no class. |
| 2 | Wrong usage. |
| 3 | The mode could not start. |
| 4 | `TEST` was given and no test of this mode ran (the class exists but its tag is not selected in this mode): `run_mode: no test of <mode> mode matches TEST=<selector>`. |
| 130 | Interrupted: `run_mode: interrupted`. |

`make` reports a non-zero status of the script as `make: *** [Makefile:<n>: check] Error
<status>` and itself exits with 2.

**(measured, experiments 4 and 5)** A failing test: exit 2 from `make`, summary line
with `failures=1`, nothing left behind. SIGINT to the process group while a test
blocked in `pg_sleep(300)`: `run_mode: interrupted`, `make` exit 130, container removed
0.5 s later (server mode: server process and work directory gone after 0.05 s). SIGTERM
to the group: the same, exit 143. SIGKILL of everything: the container (or the server's
work directory) stays, and the next run removes it before it starts (the sweeps of 10.3
and 10.5).

The complete file `tools/run_mode.sh` (BUILD-02; no later part edits it):

```sh
#!/bin/bash
# run_mode.sh MODE SELECTOR -- COMMAND...
#
# Runs the test command (Maven) for one mode. For a database mode it sources
# tools/mode_MODE.sh, which starts what the mode needs and names the facts the tests
# read as system properties; whatever was started is removed when this script ends:
# after a pass, a failure, Ctrl-C and SIGTERM. The command runs in the foreground, so
# a signal sent to the process group reaches it directly; the shell runs its own trap
# as soon as the command has ended.
#
# A mode file defines two functions:
#   mode_start   start the backend; append "-Dname=value" words to the array PROPS;
#                on failure print the reason with fail_start
#   mode_stop    remove everything mode_start created; safe to call twice
#
# Exit status: that of the command; 2 wrong usage; 3 the mode could not start;
# 4 a selector was given and no test ran; 130 interrupted.
set -u

usage() {
    echo "usage: run_mode.sh <unit|reference|oracle|server|native> <selector> -- <command>..." >&2
    exit 2
}

[ $# -ge 4 ] || usage
MODE=$1
SELECTOR=$2
[ "$3" = "--" ] || usage
shift 3

TOOLS=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
JDBC_DIR=$(dirname "$TOOLS")
# The repository root; a caller may preset REPO_ROOT (the design's scratch runs did).
: "${REPO_ROOT:=$(cd "$JDBC_DIR/../.." && pwd)}"
TARGET_DIR=$JDBC_DIR/target
export TOOLS JDBC_DIR REPO_ROOT TARGET_DIR MODE

case $MODE in
    unit | reference | oracle | server | native) ;;
    *) usage ;;
esac

fail_start() {
    echo "run_mode: cannot start $MODE mode: $*" >&2
    exit 3
}

# A real libpq and the loader read nothing from the caller's environment.
for name in $(compgen -e); do
    case $name in
        PG* | STUNTGRES_*) unset "$name" ;;
    esac
done

PROPS=("-Dstuntgres.test.mode=$MODE" "-Dstuntgres.test.reporoot=$REPO_ROOT")
if [ -n "$SELECTOR" ]; then
    PROPS+=("-Dtest=$SELECTOR")
fi

mode_start() { :; }
mode_stop() { :; }

STOPPED=0
cleanup() {
    if [ "$STOPPED" = 0 ]; then
        STOPPED=1
        mode_stop
    fi
}
interrupted() {
    trap - INT TERM
    echo "run_mode: interrupted" >&2
    cleanup
    exit 130
}
trap cleanup EXIT
trap interrupted INT TERM

if [ "$MODE" != unit ]; then
    [ -f "$TOOLS/mode_$MODE.sh" ] || fail_start "tools/mode_$MODE.sh does not exist yet"
    mkdir -p "$TARGET_DIR"
    # shellcheck source=/dev/null
    . "$TOOLS/mode_$MODE.sh"
    mode_start
fi

rm -f "$TARGET_DIR"/surefire-reports/TEST-*.xml
"$@" "${PROPS[@]}"
status=$?

# One summary line from the Surefire reports.
tests=0 failures=0 errors=0 skipped=0
for report in "$TARGET_DIR"/surefire-reports/TEST-*.xml; do
    [ -f "$report" ] || continue
    line=$(grep -m1 '<testsuite ' "$report")
    count() { echo "$line" | sed -n "s/.* $1=\"\([0-9]*\)\".*/\1/p"; }
    tests=$((tests + $(count tests)))
    failures=$((failures + $(count failures)))
    errors=$((errors + $(count errors)))
    skipped=$((skipped + $(count skipped)))
done
echo "stuntgres-jdbc: mode=$MODE tests=$tests failures=$failures errors=$errors skipped=$skipped"
cleanup
if [ "$status" = 0 ] && [ -n "$SELECTOR" ] && [ "$tests" = 0 ]; then
    echo "run_mode: no test of $MODE mode matches TEST=$SELECTOR" >&2
    exit 4
fi
exit "$status"
```

### 10.2 The modes

| Mode | Driver under test | Database | Needs | Tests selected (tag expression) |
|---|---|---|---|---|
| `unit` | classes alone, or the shim with stand-in libraries in child JVMs | none | nothing | `unit` |
| `reference` | pgjdbc 42.7.13 | PostgreSQL 18.6, pinned image, in a container | Docker | `reference` |
| `oracle` | this driver through libpq 18.6 | the same container setup | Docker | `db & !native-only` |
| `server` | this driver through libpq 18.6 | `cmd/stuntgres`, built from the tree | Go; Docker once, for libpq | `db & !native-only` |
| `native` | this driver through libspq | the engine in the test JVM | `build/libspq/lib/libspq.so` | `db` |

A part is ready to merge when unit, reference, oracle and server mode pass (plan J10);
native mode exists after TEST-02 and is run where a part says so.

System properties a mode hands to the test JVM (all start with `stuntgres.test.`):

| Property | Modes | Value |
|---|---|---|
| `mode` | all | the mode |
| `reporoot` | all | the repository root (absolute) |
| `basedir`, `builddir` | all (from the pom) | `bindings/jdbc` and `bindings/jdbc/target` (absolute) |
| `host`, `port` | reference, oracle | `127.0.0.1` and the published port of the container |
| `host` | server | `127.0.0.1` (the port changes with each server process) |
| `user`, `password` | reference, oracle, server | `stuntgres`, `stuntgres` |
| `database` | reference, oracle | `stuntgres_reference` (used for administration only) |
| `server.binary`, `server.workdir`, `server.pool` | server | the built binary, the run's work directory, 64 |
| `native.shim`, `native.library`, `native.prefix` | oracle, server, native | the loader's test seam (8.1) |

Outside `stuntgres.test.`: `stuntgres.jdk.home` (the JDK, 3.1). The environment variable
`PGPASSFILE` is set to an empty private file in oracle and server mode (10.4).

### 10.3 Reference and oracle mode: the PostgreSQL container (TEST-01B)

The container has the settings of the Go fixture (`reference/fixture/lifecycle.go`,
function `referenceStart`): image `referenceImage` of `reference/fixture/fixture.go`,
database `stuntgres_reference`, user and password `stuntgres`,
`POSTGRES_INITDB_ARGS=--locale=C --encoding=UTF8`, server arguments
`max_prepared_transactions=100`, `log_line_prefix=timeout-oracle|%p|%c|%e|`,
`track_commit_timestamp=off`. The fixture's optional settings (`wal_level`, preloaded
libraries, the OpenSSL configuration) are not used.

The image reference is kept in `tools/oracle-image.txt` (one line); a unit test of
TEST-01B reads `reference/fixture/fixture.go` under `stuntgres.test.reporoot` and fails
when the two strings differ.

Life cycle, as `tools/oracle_container.sh` drives it:

1. **Sweep.** List containers with the label `stuntgres.jdbc.pid`; remove each whose
   label value is the pid of a process that no longer exists.
2. **Start.**
   ```sh
   docker run -d --rm --name stuntgres-jdbc-<pid> --label stuntgres.jdbc.pid=<pid> \
     -e POSTGRES_DB=stuntgres_reference -e POSTGRES_USER=stuntgres \
     -e POSTGRES_PASSWORD=stuntgres -e 'POSTGRES_INITDB_ARGS=--locale=C --encoding=UTF8' \
     -p 127.0.0.1::5432 <image> \
     -c max_prepared_transactions=100 -c 'log_line_prefix=timeout-oracle|%p|%c|%e|' \
     -c track_commit_timestamp=off
   ```
   `<pid>` is the pid of `run_mode.sh`.
3. **Readiness**, a bounded poll: `docker exec <name> pg_isready -q -h 127.0.0.1 -U
   stuntgres -d stuntgres_reference` every 0.1 s, at most 120 s. The image's first-run
   server listens on a Unix socket only, so readiness over TCP means the final server.
4. **Verify.** `docker exec <name> psql -X -At ... -c "<facts query>"` must print
   `180006|UTF8|C|C|100` (version number, server encoding, collation, character type,
   `max_prepared_transactions`).
5. **Port.** `docker port <name> 5432/tcp`, first line, the number after the last colon.
6. **Stop.** `docker rm -f -v <name>`.

**(measured)** Start to verified: about 0.9 s; removal 0.2 to 0.5 s; a whole reference
run with the scratch tests 2.4 s. pgjdbc connects with password authentication
(SCRAM-SHA-256 is the image's default; pgjdbc carries its own implementation).

Isolation: **a database per test class.** `ContainerBackend` keeps one administrative
pgjdbc connection (obtained with `java.sql.DriverManager` and a `jdbc:postgresql:` URL;
no `org.postgresql` import) to `stuntgres_reference`. `acquire()` runs `CREATE DATABASE
"t<NNNNN>"` (a counter, five digits); `release()` runs `DROP DATABASE "t<NNNNN>" WITH
(FORCE)`, which also ends connections a failed test left open.

| **(measured, 200 rounds)** | Reference mode | Oracle mode |
|---|---|---|
| `CREATE DATABASE` | median 9.4 ms | 9.0 ms |
| connect, create a table, insert a row, close | 8.6 ms (pgjdbc) | 9.4 ms (pq classes) |
| `DROP DATABASE ... WITH (FORCE)` | 7.1 ms | 7.3 ms |
| whole round | 26.1 ms | 26.8 ms |

So isolation costs a test class about 16.5 ms. Roles, tablespaces and other cluster-wide
objects are shared by all test classes of a run in these two modes: a test that creates
one gives it a name that contains the name of its database (`TestDatabase.name()`).

The complete files:

`tools/oracle-image.txt`:

```text
postgres:18.6-bookworm@sha256:afc7e2d441324c0388fa80c3d24f733b4194a4eb7f47dd8ee2b08eb1a24a647c
```

`tools/oracle_container.sh`:

```sh
# oracle_container.sh - sourced by mode_reference.sh and mode_oracle.sh.
# The pinned PostgreSQL image in a container with the settings of the Go fixture
# (reference/fixture/lifecycle.go, function referenceStart).

ORACLE_IMAGE=$(cat "$TOOLS/oracle-image.txt")
ORACLE_LABEL=stuntgres.jdbc.pid
ORACLE_DB=stuntgres_reference
ORACLE_USER=stuntgres
ORACLE_PASSWORD=stuntgres
ORACLE_START_SECONDS=120
ORACLE_FACTS='180006|UTF8|C|C|100'
ORACLE_FACTS_SQL="SELECT current_setting('server_version_num'), current_setting('server_encoding'), datcollate, datctype, current_setting('max_prepared_transactions') FROM pg_database WHERE datname = current_database()"
ORACLE_CONTAINER=

# Remove containers left by runs whose process no longer exists.
container_sweep() {
    local name pid
    docker ps -a --filter "label=$ORACLE_LABEL" \
        --format "{{.Names}} {{.Label \"$ORACLE_LABEL\"}}" 2>/dev/null |
        while read -r name pid; do
            if [ -n "$pid" ] && ! kill -0 "$pid" 2>/dev/null; then
                docker rm -f -v "$name" >/dev/null 2>&1
            fi
        done
}

# Start the container, wait until it accepts connections, verify its settings and
# append the connection facts to PROPS.
container_start() {
    local err=$TARGET_DIR/docker.err deadline facts port
    command -v docker >/dev/null 2>&1 || fail_start "docker is not installed"
    container_sweep
    ORACLE_CONTAINER=stuntgres-jdbc-$$
    docker run -d --rm --name "$ORACLE_CONTAINER" --label "$ORACLE_LABEL=$$" \
        -e "POSTGRES_DB=$ORACLE_DB" -e "POSTGRES_USER=$ORACLE_USER" \
        -e "POSTGRES_PASSWORD=$ORACLE_PASSWORD" \
        -e 'POSTGRES_INITDB_ARGS=--locale=C --encoding=UTF8' \
        -p 127.0.0.1::5432 "$ORACLE_IMAGE" \
        -c max_prepared_transactions=100 -c 'log_line_prefix=timeout-oracle|%p|%c|%e|' \
        -c track_commit_timestamp=off >/dev/null 2>"$err" ||
        fail_start "docker run failed: $(tr '\n' ' ' <"$err")"
    deadline=$((SECONDS + ORACLE_START_SECONDS))
    until docker exec "$ORACLE_CONTAINER" pg_isready -q -h 127.0.0.1 -U "$ORACLE_USER" \
        -d "$ORACLE_DB" >/dev/null 2>&1; do
        [ "$SECONDS" -lt "$deadline" ] ||
            fail_start "the PostgreSQL container was not ready in $ORACLE_START_SECONDS s"
        sleep 0.1
    done
    facts=$(docker exec "$ORACLE_CONTAINER" psql -X -At -h 127.0.0.1 -U "$ORACLE_USER" \
        -d "$ORACLE_DB" -c "$ORACLE_FACTS_SQL" 2>"$err") ||
        fail_start "cannot query the container: $(tr '\n' ' ' <"$err")"
    [ "$facts" = "$ORACLE_FACTS" ] ||
        fail_start "the container reports $facts, expected $ORACLE_FACTS"
    port=$(docker port "$ORACLE_CONTAINER" 5432/tcp | head -n 1 | sed 's/.*://')
    [ -n "$port" ] || fail_start "the container publishes no port"
    PROPS+=("-Dstuntgres.test.host=127.0.0.1" "-Dstuntgres.test.port=$port"
        "-Dstuntgres.test.user=$ORACLE_USER" "-Dstuntgres.test.password=$ORACLE_PASSWORD"
        "-Dstuntgres.test.database=$ORACLE_DB")
}

container_stop() {
    if [ -n "$ORACLE_CONTAINER" ]; then
        docker rm -f -v "$ORACLE_CONTAINER" >/dev/null 2>&1
        ORACLE_CONTAINER=
    fi
    return 0
}
```

`tools/mode_reference.sh`:

```sh
# mode_reference.sh - reference mode: pgjdbc against the pinned PostgreSQL image.
. "$TOOLS/oracle_container.sh"

mode_start() {
    container_start
}

mode_stop() {
    container_stop
}
```

`tools/mode_oracle.sh`:

```sh
# mode_oracle.sh - oracle mode: this driver through libpq 18 against the pinned
# PostgreSQL image.
. "$TOOLS/oracle_container.sh"
. "$TOOLS/libpq18.sh"

mode_start() {
    libpq18_ensure
    container_start
}

mode_stop() {
    container_stop
}
```

`src/test/java/org/stuntgres/testing/ContainerBackend.java`:

```java
package org.stuntgres.testing;

import java.sql.Connection;
import java.sql.DriverManager;
import java.sql.SQLException;
import java.sql.Statement;
import java.util.concurrent.atomic.AtomicInteger;

/**
 * Reference and oracle mode: the PostgreSQL container that tools/oracle_container.sh
 * started. A test class gets a database of its own, created and dropped through an
 * administrative pgjdbc connection.
 */
final class ContainerBackend implements Backend {
    private final Mode mode = Mode.current();
    private final String host = Props.require("stuntgres.test.host");
    private final int port = Integer.parseInt(Props.require("stuntgres.test.port"));
    private final String user = Props.require("stuntgres.test.user");
    private final String password = Props.require("stuntgres.test.password");
    private final AtomicInteger counter = new AtomicInteger();
    private Connection admin;

    ContainerBackend() {
    }

    private synchronized Statement statement() throws SQLException {
        if (admin == null) {
            admin = DriverManager.getConnection("jdbc:postgresql://" + host + ":" + port + "/"
                + Props.require("stuntgres.test.database"), user, password);
        }
        return admin.createStatement();
    }

    @Override
    public synchronized TestDatabase acquire() throws SQLException {
        String name = String.format("t%05d", counter.incrementAndGet());
        try (Statement statement = statement()) {
            statement.execute("CREATE DATABASE \"" + name + "\"");
        }
        return new TestDatabase(mode, host, port, name, user, password);
    }

    @Override
    public synchronized void release(TestDatabase database) throws SQLException {
        try (Statement statement = statement()) {
            statement.execute("DROP DATABASE \"" + database.name() + "\" WITH (FORCE)");
        }
    }

    @Override
    public synchronized void close() {
        if (admin != null) {
            try {
                admin.close();
            } catch (SQLException e) {
                // The container is removed by the script anyway.
            }
            admin = null;
        }
    }
}
```

### 10.4 libpq 18 for oracle and server mode (TEST-01B)

Decision: both modes use the libpq of the pinned image, version 18.6, so that the
functions libpq 16 lacks are tested and every feature flag of 4.3 is true. The host's
`/lib/x86_64-linux-gnu/libpq.so.5` (16.15 here) is not used. The fallback the part
record allowed was not needed.

**(measured, experiment 3; the same finding as CPYTHON-DESIGN-PQ)** `libpq.so.5.18`
copied out of the image does not load by itself on Ubuntu 24.04: `libldap-2.5.so.0:
cannot open shared object file`. Its other dependencies exist on the host.
`libldap-2.5.so.0` needs `liblber-2.5.so.0`, which the host also lacks. Opening
`liblber-2.5.so.0`, then `libldap-2.5.so.0`, then `libpq.so.5.18` by absolute path works
inside the JVM: the dynamic linker satisfies a `DT_NEEDED` entry from an object that is
already loaded under the same soname. Nothing is installed and no search path is
changed. That is what the dependency list of the loader's test seam is for.

`tools/libpq18.sh` copies the three files to `target/libpq18` unless
`target/libpq18/image.txt` already names the pinned image:

```sh
docker create <image>                                      # prints the container id
docker cp -L <id>:/usr/lib/<triplet>/<file> target/libpq18/<file>     # three times
docker rm -f <id>
```

`<triplet>` is `x86_64-linux-gnu` or `aarch64-linux-gnu` by `uname -m` (`x86_64` was
run). It then creates the empty file `target/pgpass` with mode 0600, exports
`PGPASSFILE` (a real libpq reads the password file of the user and complains about loose
permissions) and appends the three test-seam properties. The complete file:

```sh
# libpq18.sh - sourced by mode_oracle.sh and mode_server.sh.
# libpq 18 for the modes that test the driver through a real libpq: the library of
# the pinned image and the two libraries it needs that Ubuntu 24.04 lacks, copied to
# target/libpq18. Nothing is installed and no search path is changed: the loader
# opens the three files by absolute path, dependencies first.

LIBPQ18_FILES="liblber-2.5.so.0 libldap-2.5.so.0 libpq.so.5.18"

# Copy the files unless target/libpq18/image.txt names the pinned image already,
# then append the loader's test-seam properties to PROPS.
libpq18_ensure() {
    local image dest stamp triplet container file list=""
    image=$(cat "$TOOLS/oracle-image.txt")
    dest=$TARGET_DIR/libpq18
    stamp=$dest/image.txt
    case $(uname -m) in
        x86_64) triplet=x86_64-linux-gnu ;;
        aarch64) triplet=aarch64-linux-gnu ;;
        *) fail_start "no libpq 18 for $(uname -m)" ;;
    esac
    if [ ! -f "$stamp" ] || [ "$(cat "$stamp")" != "$image" ]; then
        command -v docker >/dev/null 2>&1 || fail_start "docker is not installed"
        mkdir -p "$dest"
        container=$(docker create "$image" 2>"$TARGET_DIR/docker.err") ||
            fail_start "docker create failed: $(tr '\n' ' ' <"$TARGET_DIR/docker.err")"
        for file in $LIBPQ18_FILES; do
            docker cp -L "$container:/usr/lib/$triplet/$file" "$dest/$file" >/dev/null 2>&1 || {
                docker rm -f "$container" >/dev/null 2>&1
                fail_start "cannot copy $file out of the image"
            }
        done
        docker rm -f "$container" >/dev/null 2>&1
        printf '%s' "$image" >"$stamp"
    fi
    for file in $LIBPQ18_FILES; do
        list=$list${list:+:}$dest/$file
    done
    # A real libpq reads the password file; point it at an empty private one.
    (umask 077 && : >"$TARGET_DIR/pgpass")
    export PGPASSFILE=$TARGET_DIR/pgpass
    PROPS+=("-Dstuntgres.test.native.shim=$TARGET_DIR/native/libstuntgres_jni.so"
        "-Dstuntgres.test.native.library=$list" "-Dstuntgres.test.native.prefix=PQ")
}
```

### 10.5 Server mode (TEST-01C)

Facts of the wire server that shape this mode (`cmd/stuntgres/README.md` "Operation",
`pgserver/README.md` "Configuration and ownership", `internal/serverauth/users.go`
function `Load`, `internal/serverauth/password_cost_reduced.go`):

- Accounts come from a user file with records `name:$pbkdf2-sha256$i=<n>$<salt>$<key>:
  <db1>,<db2>,...`; the file must be mode 0600. The build tag `stuntgres_reduced_kdf`
  lowers the PBKDF2 cost from 600000 to 4096 iterations for test servers. Password and
  salt of the test account are constants, so the record is a constant of the script (no
  tool is needed to compute it).
- The served databases are exactly those the user file grants; each is an engine of its
  own with its own roles. A database created with SQL cannot be connected to.
- `serve -l 127.0.0.1:0` binds an ephemeral port and prints one JSON line when ready:
  `{"tstamp":...,"severity":"INFO","message":"server started","address":"127.0.0.1:<port>","databases":[...]}`.
  SQL errors are logged to standard output too, so it goes to a file.
- Authentication is a cleartext password; pgjdbc and libpq both connect **(measured)**.

**(measured)** A server costs 3 ms and 3 MB per served database at start:

| Databases | 1 | 16 | 64 | 256 | 512 | 1024 |
|---|---|---|---|---|---|---|
| start until "server started" | 0.10 s | 0.14 s | 0.25 s | 0.83 s | 1.6 s | 3.3 s |
| resident memory | 65 MB | 106 MB | 249 MB | 830 MB | 1.6 GB | 3.1 GB |

Decision: **a rolling pool of 64 databases.** The user file grants `t000` to `t063`.
A test class gets the next unused one; when all are used the server is replaced by a
new process (same user file, new port, 64 fresh engines). Memory stays at a quarter of
a gigabyte however many test classes the suite has. `release` does nothing.

| **(measured, 200 rounds)** | Server mode |
|---|---|
| acquire | 0.0 ms median; 172 ms when a new server starts (every 64th) |
| connect, create a table, insert a row, close | 1.6 ms |
| whole round | 5.2 ms |

Who does what: `tools/mode_server.sh` builds the server, removes what dead runs left,
creates the work directory `target/server-<pid>` with the user file and names it to the
test JVM; `ServerBackend` (in the test JVM) starts and replaces server processes and
writes each process id to `<workdir>/server.pid`; `mode_stop` kills the process recorded
there (if its command line contains the work directory) and removes the directory.
**(measured)** After a pass, a failure, SIGINT and SIGTERM nothing is left; after SIGKILL
of everything the work directory stays and the next run's sweep removes it.

```sh
go build -tags stuntgres_reduced_kdf -o bindings/jdbc/target/stuntgres ./cmd/stuntgres   # cwd: repository root
target/stuntgres serve -l 127.0.0.1:0 -u <workdir>/users -p <workdir>/stuntgres-<n>.pidfile \
    > <workdir>/server-<n>.log 2>&1
```

**(measured)** The build takes about 50 s cold and well under a second when nothing
changed; a server-mode run with the scratch tests 1.8 s.

Decided by experiment (the part record left open whether a script or the test JVM starts
the backends): the container is started by the script (one process for the whole run, removal
guaranteed by the shell's traps); the server processes are started by the test JVM
because they must be replaced during the run, with the script as the safety net.

`tools/mode_server.sh`:

```sh
# mode_server.sh - server mode: this driver through libpq 18 against cmd/stuntgres.
# This script builds the server and prepares its account file; the test JVM starts
# and restarts server processes (org.stuntgres.testing.ServerBackend) and records
# every process in the work directory, which mode_stop clears.
. "$TOOLS/libpq18.sh"

SERVER_POOL=64
SERVER_WORKDIR=
# The account stuntgres with password stuntgres: PBKDF2-HMAC-SHA256, 4096 iterations
# (the cost of the build tag stuntgres_reduced_kdf), salt "stuntgres-jdbc-0".
SERVER_VERIFIER='$pbkdf2-sha256$i=4096$c3R1bnRncmVzLWpkYmMtMA$UpbRp90l4rST2l0vzUE9y9lS6F5yUM1/kPUl1yF19zQ'

# Kill the servers recorded in a work directory and remove it.
server_clear() {
    local dir=$1 file pid
    for file in "$dir"/*.pid; do
        [ -f "$file" ] || continue
        pid=$(tr -cd '0-9' <"$file")
        if [ -n "$pid" ] && grep -qa -- "$dir" "/proc/$pid/cmdline" 2>/dev/null; then
            kill -9 "$pid" 2>/dev/null
        fi
    done
    rm -rf -- "$dir"
}

# Clear the work directories of runs whose process no longer exists.
server_sweep() {
    local dir pid
    for dir in "$TARGET_DIR"/server-*; do
        [ -d "$dir" ] || continue
        pid=${dir##*-}
        case $pid in
            '' | *[!0-9]*) continue ;;
        esac
        kill -0 "$pid" 2>/dev/null || server_clear "$dir"
    done
}

mode_start() {
    local names i
    libpq18_ensure
    command -v go >/dev/null 2>&1 || fail_start "go is not installed"
    (cd "$REPO_ROOT" && go build -tags stuntgres_reduced_kdf -o "$TARGET_DIR/stuntgres" \
        ./cmd/stuntgres) >"$TARGET_DIR/go-build.err" 2>&1 ||
        fail_start "go build ./cmd/stuntgres failed: $(tail -n 5 "$TARGET_DIR/go-build.err" | tr '\n' ' ')"
    server_sweep
    SERVER_WORKDIR=$TARGET_DIR/server-$$
    mkdir -p "$SERVER_WORKDIR"
    names=t000
    for ((i = 1; i < SERVER_POOL; i++)); do
        names=$names,$(printf 't%03d' "$i")
    done
    (umask 077 && printf 'stuntgres:%s:%s\n' "$SERVER_VERIFIER" "$names" >"$SERVER_WORKDIR/users")
    PROPS+=("-Dstuntgres.test.server.binary=$TARGET_DIR/stuntgres"
        "-Dstuntgres.test.server.workdir=$SERVER_WORKDIR"
        "-Dstuntgres.test.server.pool=$SERVER_POOL"
        "-Dstuntgres.test.host=127.0.0.1"
        "-Dstuntgres.test.user=stuntgres" "-Dstuntgres.test.password=stuntgres")
}

mode_stop() {
    if [ -n "$SERVER_WORKDIR" ]; then
        server_clear "$SERVER_WORKDIR"
        SERVER_WORKDIR=
    fi
    return 0
}
```

`src/test/java/org/stuntgres/testing/ServerBackend.java`:

```java
package org.stuntgres.testing;

import java.io.File;
import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.Paths;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

/**
 * Server mode: the stuntgres wire server that tools/mode_server.sh built. The account
 * file grants a fixed pool of databases, each an engine of its own; a test class gets
 * the next unused one, and when the pool is used up the server is replaced by a new
 * process with fresh engines.
 */
final class ServerBackend implements Backend {
    private static final Pattern STARTED =
        Pattern.compile("\"server started\",\"address\":\"127\\.0\\.0\\.1:(\\d+)\"");
    private static final long START_MILLIS = 60_000;

    private final String binary = Props.require("stuntgres.test.server.binary");
    private final Path workdir = Paths.get(Props.require("stuntgres.test.server.workdir"));
    private final int pool = Integer.parseInt(Props.require("stuntgres.test.server.pool"));
    private final String host = Props.require("stuntgres.test.host");
    private final String user = Props.require("stuntgres.test.user");
    private final String password = Props.require("stuntgres.test.password");
    private Process process;
    private int port;
    private int used;
    private int generation;

    ServerBackend() {
    }

    private void spawn() throws IOException {
        kill();
        generation++;
        Path log = workdir.resolve("server-" + generation + ".log");
        ProcessBuilder builder = new ProcessBuilder(binary, "serve", "-l", "127.0.0.1:0",
            "-u", workdir.resolve("users").toString(),
            "-p", workdir.resolve("stuntgres-" + generation + ".pidfile").toString());
        builder.redirectErrorStream(true);
        builder.redirectOutput(log.toFile());
        builder.redirectInput(new File("/dev/null"));
        process = builder.start();
        Files.write(workdir.resolve("server.pid"),
            Long.toString(process.pid()).getBytes(StandardCharsets.US_ASCII));
        long deadline = System.currentTimeMillis() + START_MILLIS;
        for (;;) {
            String text = new String(Files.readAllBytes(log), StandardCharsets.UTF_8);
            Matcher matcher = STARTED.matcher(text);
            if (matcher.find()) {
                port = Integer.parseInt(matcher.group(1));
                used = 0;
                return;
            }
            if (!process.isAlive() || System.currentTimeMillis() > deadline) {
                kill();
                throw new IOException("the stuntgres server did not start: " + text.trim());
            }
            Waits.pause(5);
        }
    }

    private void kill() {
        if (process != null) {
            process.destroyForcibly();
            try {
                process.waitFor();
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
            }
            process = null;
        }
    }

    @Override
    public synchronized TestDatabase acquire() throws IOException {
        if (process == null || used == pool) {
            spawn();
        }
        String name = String.format("t%03d", used++);
        return new TestDatabase(Mode.SERVER, host, port, name, user, password);
    }

    @Override
    public void release(TestDatabase database) {
        // Nothing to do: a database is never handed out twice.
    }

    @Override
    public synchronized void close() {
        kill();
    }
}
```

Differences between the stuntgres server and PostgreSQL 18.6 that the scratch tests of
this design met **(measured)**: `server_version` is `18.0` and `serverVersion()` 180000
(docs/LIBSPQ.md 5.2); the error fields `F`, `L`, `R` are absent; no `DEBUG` notices with
`client_min_messages=debug5`; a session whose client vanished while it executes
`pg_sleep` ends at once. None is a statement or call sequence that succeeds on
PostgreSQL and fails or answers differently in a way the binding or a designed test
depends on, so no engine part was needed (rule D8). Tests assert none of these.

### 10.6 Support API (BUILD-02, TEST-01A)

Package `org.stuntgres.testing` under `src/test/java`.

| Name | Part | Purpose |
|---|---|---|
| `@UnitTest` | BUILD-02 | Class annotation: tag `unit`. The class needs no database and no real target library. |
| `Mode` | BUILD-02 | Enum `UNIT`, `REFERENCE`, `ORACLE`, `SERVER`, `NATIVE`; `Mode.current()` reads `stuntgres.test.mode`. |
| `Waits.until(long timeoutMillis, String what, BooleanSupplier condition)` | BUILD-02 | Bounded wait: checks the condition every 5 ms; `AssertionError` `timed out after <n> ms waiting for <what>` when it does not hold in time. |
| `ChildJvm.run(Class<?> mainClass, Map<String, String> properties, List<String> extraClassPath, String... arguments)` | BUILD-02 | Runs a main class in a fresh JVM (same JDK, same class path with `extraClassPath` in front, `-Xcheck:jni`), waits at most 60 s, returns exit code and combined output. For code with process-wide state: the loader. |
| `@DatabaseTest` | TEST-01A | Class annotation: tag `db` and the extension. The class gets a database of its own. |
| `@ReferenceToo` | TEST-01A | Class annotation: tag `reference`. The class also runs in reference mode. |
| `@NativeOnly` | TEST-01A | Class annotation: tag `native-only`. The class runs in native mode only. |
| `TestDatabase` | TEST-01A | What a test gets: `mode()`, `host()`, `port()`, `name()`, `user()`, `password()`, `conninfo()`, `jdbcUrl()`, `connect()`, `connect(Properties)`. |
| `DatabaseExtension` | TEST-01A | Acquires the class's `TestDatabase` before its first test and releases it after its last; resolves parameters of type `TestDatabase` in constructors, lifecycle methods and test methods. |
| `Backend`, `Props` | TEST-01A | The contract of a mode's backend (`acquire`, `release`, `close`); required system properties. |

How a test gets its connection facts:

```java
@DatabaseTest
class ConnExecTest {
    @Test
    void execReturnsTheLastResult(TestDatabase db) throws Exception {
        try (PqConnection conn = PqConnection.connect(db.conninfo())) {
            ...
        }
    }
}
```

- Below the JDBC layer (this foundation) a test uses `db.conninfo()`: a libpq connection
  string built with `Conninfo` from host, port, database, user, password and
  `sslmode=disable`. It raises `IllegalStateException` in reference mode, where no libpq
  is involved.
- From JDBC-DESIGN-CORE on a test uses `db.connect()`: a `java.sql.Connection` from
  `DriverManager` for `jdbc:postgresql://<host>:<port>/<database>` in reference mode
  (pgjdbc) and `jdbc:stuntgres://<host>:<port>/<database>` otherwise, with the
  properties `user` and `password`. One test body then serves all modes. In native mode
  `<host>` is the cluster name. (JDBC-DESIGN-CORE defines the driver so that it accepts
  exactly this URL and these two properties; until the driver exists `connect()` works
  in reference mode only.)
- The database is the class's own (10.3, 10.5, 10.8). Nested test classes share the
  database of their outermost class. The extension keeps the backend of the run in
  JUnit's root store and closes it when the run ends.

The backend classes are created by name (`ContainerBackend`, `ServerBackend`,
`NativeBackend`), because each belongs to its own part; a mode whose class does not
exist yet fails with `<MODE> mode is not available yet: class <name> is missing or
cannot be created`.

The complete files of BUILD-02:

```java
package org.stuntgres.testing;

import java.lang.annotation.ElementType;
import java.lang.annotation.Retention;
import java.lang.annotation.RetentionPolicy;
import java.lang.annotation.Target;
import org.junit.jupiter.api.Tag;

/** A test class that needs neither a database nor a real target library. */
@Target(ElementType.TYPE)
@Retention(RetentionPolicy.RUNTIME)
@Tag("unit")
public @interface UnitTest {
}
```

```java
package org.stuntgres.testing;

import java.util.Locale;

/** The test mode of the run: the value of {@code make check MODE=...}. */
public enum Mode {
    /** No database and no real target library. */
    UNIT,
    /** pgjdbc against PostgreSQL 18.6. */
    REFERENCE,
    /** This driver through libpq 18 against PostgreSQL 18.6. */
    ORACLE,
    /** This driver through libpq 18 against the stuntgres wire server. */
    SERVER,
    /** This driver through libspq. */
    NATIVE;

    /**
     * Returns the mode of this JVM (system property {@code stuntgres.test.mode}).
     *
     * @return the mode; UNIT when the property is absent
     */
    public static Mode current() {
        return valueOf(System.getProperty("stuntgres.test.mode", "unit").toUpperCase(Locale.ROOT));
    }
}
```

```java
package org.stuntgres.testing;

import java.util.concurrent.locks.LockSupport;
import java.util.function.BooleanSupplier;

/** Bounded waits. Tests never sleep for a fixed time to synchronize. */
public final class Waits {
    private Waits() {
    }

    /**
     * Waits until a condition holds, checking it every few milliseconds.
     *
     * @param timeoutMillis the bound
     * @param what names the condition in the failure message
     * @param condition the condition
     * @throws AssertionError when the condition does not hold in time
     */
    public static void until(long timeoutMillis, String what, BooleanSupplier condition) {
        long deadline = System.nanoTime() + timeoutMillis * 1_000_000L;
        while (!condition.getAsBoolean()) {
            if (System.nanoTime() > deadline) {
                throw new AssertionError("timed out after " + timeoutMillis + " ms waiting for "
                    + what);
            }
            pause(5);
        }
    }

    /** One step of a bounded polling loop. */
    static void pause(long millis) {
        LockSupport.parkNanos(millis * 1_000_000L);
    }
}
```

```java
package org.stuntgres.testing;

import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.concurrent.TimeUnit;

/**
 * Runs a main class in a fresh JVM. Tests of code that keeps state for the whole
 * process (the native loader) use it, because the test JVM is shared by all tests.
 */
public final class ChildJvm {
    /** What a child JVM left behind. */
    public static final class Outcome {
        private final int exitCode;
        private final String output;

        Outcome(int exitCode, String output) {
            this.exitCode = exitCode;
            this.output = output;
        }

        /**
         * Returns the exit code.
         *
         * @return the exit code of the JVM
         */
        public int exitCode() {
            return exitCode;
        }

        /**
         * Returns standard output and standard error, in the order written.
         *
         * @return the text
         */
        public String output() {
            return output;
        }
    }

    private ChildJvm() {
    }

    /**
     * Runs {@code mainClass} with the class path of the test JVM.
     *
     * @param mainClass the class with the main method
     * @param properties system properties of the child
     * @param extraClassPath directories or jars put in front of the class path
     * @param arguments program arguments
     * @return exit code and output
     * @throws IOException when the JVM cannot be started or does not end in 60 s
     */
    public static Outcome run(Class<?> mainClass, Map<String, String> properties,
            List<String> extraClassPath, String... arguments) throws IOException {
        List<String> command = new ArrayList<>();
        command.add(Path.of(System.getProperty("java.home"), "bin", "java").toString());
        command.add("--enable-native-access=ALL-UNNAMED");
        command.add("-Xcheck:jni");
        StringBuilder classPath = new StringBuilder();
        for (String entry : extraClassPath) {
            classPath.append(entry).append(':');
        }
        classPath.append(System.getProperty("surefire.test.class.path",
            System.getProperty("java.class.path")));
        command.add("-cp");
        command.add(classPath.toString());
        for (Map.Entry<String, String> property : properties.entrySet()) {
            command.add("-D" + property.getKey() + "=" + property.getValue());
        }
        command.add(mainClass.getName());
        for (String argument : arguments) {
            command.add(argument);
        }
        Path output = Files.createTempFile("stuntgres-child-", ".out");
        try {
            ProcessBuilder builder = new ProcessBuilder(command);
            builder.redirectErrorStream(true);
            builder.redirectOutput(output.toFile());
            Process process = builder.start();
            try {
                if (!process.waitFor(60, TimeUnit.SECONDS)) {
                    process.destroyForcibly();
                    throw new IOException("the child JVM did not end in 60 s");
                }
            } catch (InterruptedException e) {
                process.destroyForcibly();
                Thread.currentThread().interrupt();
                throw new IOException("interrupted while waiting for the child JVM", e);
            }
            return new Outcome(process.exitValue(),
                new String(Files.readAllBytes(output), StandardCharsets.UTF_8));
        } finally {
            Files.deleteIfExists(output);
        }
    }
}
```

The complete files of TEST-01A:

```java
package org.stuntgres.testing;

import java.lang.annotation.ElementType;
import java.lang.annotation.Inherited;
import java.lang.annotation.Retention;
import java.lang.annotation.RetentionPolicy;
import java.lang.annotation.Target;
import org.junit.jupiter.api.Tag;
import org.junit.jupiter.api.extension.ExtendWith;

/**
 * A test class that needs a database. The class gets isolated database state of its
 * own: declare a parameter of type {@link TestDatabase} in the constructor, a
 * lifecycle method or a test method. Runs in oracle, server and native mode, and in
 * reference mode when the class also carries {@link ReferenceToo}.
 */
@Target(ElementType.TYPE)
@Retention(RetentionPolicy.RUNTIME)
@Inherited
@Tag("db")
@ExtendWith(DatabaseExtension.class)
public @interface DatabaseTest {
}
```

```java
package org.stuntgres.testing;

import java.lang.annotation.ElementType;
import java.lang.annotation.Inherited;
import java.lang.annotation.Retention;
import java.lang.annotation.RetentionPolicy;
import java.lang.annotation.Target;
import org.junit.jupiter.api.Tag;

/** Marks a {@link DatabaseTest} class whose tests also run through pgjdbc in reference mode. */
@Target(ElementType.TYPE)
@Retention(RetentionPolicy.RUNTIME)
@Inherited
@Tag("reference")
public @interface ReferenceToo {
}
```

```java
package org.stuntgres.testing;

import java.lang.annotation.ElementType;
import java.lang.annotation.Inherited;
import java.lang.annotation.Retention;
import java.lang.annotation.RetentionPolicy;
import java.lang.annotation.Target;
import org.junit.jupiter.api.Tag;

/** Marks a {@link DatabaseTest} class that runs in native mode only. */
@Target(ElementType.TYPE)
@Retention(RetentionPolicy.RUNTIME)
@Inherited
@Tag("native-only")
public @interface NativeOnly {
}
```

```java
package org.stuntgres.testing;

/** Hands out isolated database state for the mode of the run. */
interface Backend extends AutoCloseable {
    /**
     * Creates database state no other test class sees.
     *
     * @return the facts to connect to it
     * @throws Exception when the backend cannot provide it
     */
    TestDatabase acquire() throws Exception;

    /**
     * Removes what {@link #acquire()} created.
     *
     * @param database a value {@code acquire} returned
     * @throws Exception when removal fails
     */
    void release(TestDatabase database) throws Exception;

    /** Stops the backend. Never throws; may be called twice. */
    @Override
    void close();
}
```

```java
package org.stuntgres.testing;

/** System properties the mode scripts pass to the test JVM. */
final class Props {
    private Props() {
    }

    /**
     * Returns a property the mode script must have set.
     *
     * @param name the property name
     * @return its value
     */
    static String require(String name) {
        String value = System.getProperty(name);
        if (value == null || value.isEmpty()) {
            throw new IllegalStateException("system property " + name + " is not set; run the"
                + " tests with make -C bindings/jdbc check MODE=<mode>");
        }
        return value;
    }
}
```

```java
package org.stuntgres.testing;

import java.sql.Connection;
import java.sql.DriverManager;
import java.sql.SQLException;
import java.util.Properties;
import org.stuntgres.pq.Conninfo;

/** Isolated database state of one test class, with the facts needed to connect to it. */
public final class TestDatabase {
    private final Mode mode;
    private final String host;
    private final int port;
    private final String name;
    private final String user;
    private final String password;

    TestDatabase(Mode mode, String host, int port, String name, String user, String password) {
        this.mode = mode;
        this.host = host;
        this.port = port;
        this.name = name;
        this.user = user;
        this.password = password;
    }

    /**
     * Returns the mode of the run.
     *
     * @return the mode
     */
    public Mode mode() {
        return mode;
    }

    /**
     * Returns the host: an address, or in native mode the cluster name.
     *
     * @return the host
     */
    public String host() {
        return host;
    }

    /**
     * Returns the port.
     *
     * @return the port
     */
    public int port() {
        return port;
    }

    /**
     * Returns the database name.
     *
     * @return the name
     */
    public String name() {
        return name;
    }

    /**
     * Returns the role to connect as.
     *
     * @return the user name
     */
    public String user() {
        return user;
    }

    /**
     * Returns the password of {@link #user()}.
     *
     * @return the password
     */
    public String password() {
        return password;
    }

    /**
     * Returns the libpq connection string for {@code org.stuntgres.pq.PqConnection}.
     * Not available in reference mode.
     *
     * @return the connection string
     */
    public String conninfo() {
        if (mode == Mode.REFERENCE) {
            throw new IllegalStateException("no libpq connection in reference mode");
        }
        return new Conninfo().add("host", host).add("port", Integer.toString(port))
            .add("dbname", name).add("user", user).add("password", password)
            .add("sslmode", "disable").toString();
    }

    /**
     * Returns the JDBC URL: pgjdbc's in reference mode, this driver's otherwise.
     *
     * @return the URL without user and password
     */
    public String jdbcUrl() {
        String scheme = mode == Mode.REFERENCE ? "postgresql" : "stuntgres";
        return "jdbc:" + scheme + "://" + host + ":" + port + "/" + name;
    }

    /**
     * Opens a JDBC connection: through pgjdbc in reference mode, through this driver
     * otherwise.
     *
     * @return a new connection; the caller closes it
     * @throws SQLException when the connection fails
     */
    public Connection connect() throws SQLException {
        return connect(new Properties());
    }

    /**
     * Opens a JDBC connection with additional properties.
     *
     * @param extra properties added to user and password
     * @return a new connection; the caller closes it
     * @throws SQLException when the connection fails
     */
    public Connection connect(Properties extra) throws SQLException {
        Properties properties = new Properties();
        properties.putAll(extra);
        properties.setProperty("user", user);
        properties.setProperty("password", password);
        return DriverManager.getConnection(jdbcUrl(), properties);
    }
}
```

```java
package org.stuntgres.testing;

import org.junit.jupiter.api.extension.BeforeAllCallback;
import org.junit.jupiter.api.extension.ExtensionContext;
import org.junit.jupiter.api.extension.ExtensionContext.Namespace;
import org.junit.jupiter.api.extension.ExtensionContext.Store;
import org.junit.jupiter.api.extension.ParameterContext;
import org.junit.jupiter.api.extension.ParameterResolver;

/**
 * Gives a {@link DatabaseTest} class its {@link TestDatabase}: acquired from the
 * backend of the mode before the first test of the class, released after the last.
 */
public final class DatabaseExtension implements BeforeAllCallback, ParameterResolver {
    private static final Namespace NAMESPACE = Namespace.create(DatabaseExtension.class);

    /** The backend of the run; closed by JUnit when the whole run ends. */
    private static final class BackendHolder implements Store.CloseableResource {
        private final Backend backend;

        BackendHolder(Backend backend) {
            this.backend = backend;
        }

        @Override
        public void close() {
            backend.close();
        }
    }

    /** The database of one class; released by JUnit when the class ends. */
    private static final class Lease implements Store.CloseableResource {
        private final Backend backend;
        private final TestDatabase database;

        Lease(Backend backend, TestDatabase database) {
            this.backend = backend;
            this.database = database;
        }

        @Override
        public void close() throws Exception {
            backend.release(database);
        }
    }

    /** Created by JUnit. */
    public DatabaseExtension() {
    }

    /** The backend class of a mode; loaded by name because each belongs to its own part. */
    private static Backend create(Mode mode) {
        String name;
        switch (mode) {
            case REFERENCE:
            case ORACLE:
                name = "ContainerBackend";
                break;
            case SERVER:
                name = "ServerBackend";
                break;
            case NATIVE:
                name = "NativeBackend";
                break;
            default:
                throw new IllegalStateException("a @DatabaseTest class cannot run in " + mode
                    + " mode");
        }
        try {
            return (Backend) Class.forName("org.stuntgres.testing." + name)
                .getDeclaredConstructor().newInstance();
        } catch (ReflectiveOperationException e) {
            throw new IllegalStateException(mode + " mode is not available yet: class " + name
                + " is missing or cannot be created", e);
        }
    }

    private static Backend backend(ExtensionContext context) {
        return context.getRoot().getStore(NAMESPACE)
            .getOrComputeIfAbsent("backend", key -> new BackendHolder(create(Mode.current())),
                BackendHolder.class).backend;
    }

    private static TestDatabase database(ExtensionContext context) {
        Backend backend = backend(context);
        return context.getStore(NAMESPACE).getOrComputeIfAbsent("database", key -> {
            try {
                return new Lease(backend, backend.acquire());
            } catch (Exception e) {
                throw new IllegalStateException("cannot create the test database", e);
            }
        }, Lease.class).database;
    }

    @Override
    public void beforeAll(ExtensionContext context) {
        database(context);
    }

    @Override
    public boolean supportsParameter(ParameterContext parameter, ExtensionContext context) {
        return parameter.getParameter().getType() == TestDatabase.class;
    }

    @Override
    public Object resolveParameter(ParameterContext parameter, ExtensionContext context) {
        return database(context);
    }
}
```

### 10.7 Tags and gates (TEST-01G)

Every test class carries exactly one of `@UnitTest` and `@DatabaseTest`; a database
class may add `@ReferenceToo` or `@NativeOnly`. A class without a tag would never run:
`TagAuditTest` (a unit test) walks `target/test-classes` and fails for a top-level class
with `@Test` methods that does not carry exactly one of the two.

There are two gates, and no other way to skip a test:

| Gate | On | Effect |
|---|---|---|
| `@Modes({Mode.A, ...})` | class or method | Runs only in the listed modes; elsewhere it is reported as skipped (`not run in <MODE> mode`). |
| `@RequiresFeature(Feature.X)` | class or method | Runs only when the loaded library has the optional function (the gate calls `NativeLoader.ensureLoaded()`); skipped in unit and reference mode (`no target library in <MODE> mode`) and when the function is absent (`the loaded library has no <member>`). |

Both are evaluated by `GateCondition`. With libpq 18 and with libspq every libpq feature
is present, so `@RequiresFeature` skips nothing in the four database modes today; it
exists for the `EXT_*` features and for a harness run against an older libpq.

**Tests of the mirrored extension API** (`org.stuntgres.PGConnection`,
`org.stuntgres.util.PGobject` and the others of plan J9). Decision: they are excluded
from reference mode (no `@ReferenceToo`), and the design part that specifies such a
class records its literal expectations from pgjdbc with scratch code, as rule D7
demands. A facade inside the harness was rejected: test code may not import
`org.postgresql`, the two APIs are distinct types in distinct packages, and several of
them are classes (`PGobject`, `PGInterval`), which a reflective proxy cannot stand in
for. Tests of `java.sql` behavior are not affected: they use `db.connect()` and run in
reference mode.

The complete files:

```java
package org.stuntgres.testing;

import java.lang.annotation.ElementType;
import java.lang.annotation.Retention;
import java.lang.annotation.RetentionPolicy;
import java.lang.annotation.Target;
import org.junit.jupiter.api.extension.ExtendWith;

/** Runs a test class or method only in the listed modes; elsewhere it is reported as skipped. */
@Target({ElementType.TYPE, ElementType.METHOD})
@Retention(RetentionPolicy.RUNTIME)
@ExtendWith(GateCondition.class)
public @interface Modes {
    /**
     * The modes the test runs in.
     *
     * @return one or more modes
     */
    Mode[] value();
}
```

```java
package org.stuntgres.testing;

import java.lang.annotation.ElementType;
import java.lang.annotation.Retention;
import java.lang.annotation.RetentionPolicy;
import java.lang.annotation.Target;
import org.junit.jupiter.api.extension.ExtendWith;

/**
 * Runs a test class or method only when the loaded target library has an optional
 * function; elsewhere (and in reference mode) it is reported as skipped.
 */
@Target({ElementType.TYPE, ElementType.METHOD})
@Retention(RetentionPolicy.RUNTIME)
@ExtendWith(GateCondition.class)
public @interface RequiresFeature {
    /**
     * The feature the test needs.
     *
     * @return a constant of {@code org.stuntgres.pq.Feature}
     */
    int value();
}
```

```java
package org.stuntgres.testing;

import java.util.Arrays;
import java.util.Optional;
import org.junit.jupiter.api.extension.ConditionEvaluationResult;
import org.junit.jupiter.api.extension.ExecutionCondition;
import org.junit.jupiter.api.extension.ExtensionContext;
import org.junit.platform.commons.support.AnnotationSupport;
import org.stuntgres.pq.Feature;
import org.stuntgres.pq.NativeLoader;
import org.stuntgres.pq.PqException;
import org.stuntgres.pq.PqLibrary;

/** Evaluates {@link Modes} and {@link RequiresFeature}: the only skips of the suite. */
public final class GateCondition implements ExecutionCondition {
    /** Created by JUnit. */
    public GateCondition() {
    }

    @Override
    public ConditionEvaluationResult evaluateExecutionCondition(ExtensionContext context) {
        Optional<Modes> modes = AnnotationSupport.findAnnotation(context.getElement(), Modes.class);
        Mode mode = Mode.current();
        if (modes.isPresent() && !Arrays.asList(modes.get().value()).contains(mode)) {
            return ConditionEvaluationResult.disabled("not run in " + mode + " mode");
        }
        Optional<RequiresFeature> feature =
            AnnotationSupport.findAnnotation(context.getElement(), RequiresFeature.class);
        if (feature.isPresent()) {
            int wanted = feature.get().value();
            if (mode == Mode.UNIT || mode == Mode.REFERENCE) {
                return ConditionEvaluationResult.disabled("no target library in " + mode + " mode");
            }
            try {
                NativeLoader.ensureLoaded();
            } catch (PqException e) {
                throw new IllegalStateException(e);
            }
            if (!PqLibrary.hasFeature(wanted)) {
                return ConditionEvaluationResult.disabled(
                    "the loaded library has no " + Feature.name(wanted));
            }
        }
        return ConditionEvaluationResult.enabled("gates passed");
    }
}
```

```java
package org.stuntgres.testing;

import static org.junit.jupiter.api.Assertions.assertEquals;

import java.io.IOException;
import java.lang.reflect.Method;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.Paths;
import java.util.ArrayList;
import java.util.List;
import java.util.stream.Stream;
import org.junit.jupiter.api.Test;

/** Every test class carries exactly one of {@link UnitTest} and {@link DatabaseTest}. */
@UnitTest
class TagAuditTest {
    @Test
    void everyTestClassIsTagged() throws Exception {
        Path root = Paths.get(System.getProperty("stuntgres.test.builddir"), "test-classes");
        List<String> wrong = new ArrayList<>();
        List<Path> files = new ArrayList<>();
        try (Stream<Path> walk = Files.walk(root)) {
            walk.filter(p -> p.toString().endsWith(".class")).forEach(files::add);
        } catch (IOException e) {
            throw new AssertionError(e);
        }
        for (Path file : files) {
            String name = root.relativize(file).toString().replace('/', '.');
            Class<?> type = Class.forName(name.substring(0, name.length() - 6), false,
                TagAuditTest.class.getClassLoader());
            if (type.getEnclosingClass() != null || !hasTests(type)) {
                continue;
            }
            int tags = (type.isAnnotationPresent(UnitTest.class) ? 1 : 0)
                + (type.isAnnotationPresent(DatabaseTest.class) ? 1 : 0);
            if (tags != 1) {
                wrong.add(type.getName());
            }
            if (type.isAnnotationPresent(UnitTest.class)
                    && (type.isAnnotationPresent(ReferenceToo.class)
                        || type.isAnnotationPresent(NativeOnly.class))) {
                wrong.add(type.getName() + " (a unit test with a database-mode tag)");
            }
        }
        assertEquals(List.of(), wrong, "test classes without exactly one of @UnitTest and"
            + " @DatabaseTest never run in the intended mode");
    }

    private static boolean hasTests(Class<?> type) {
        for (Class<?> c = type; c != null && c != Object.class; c = c.getSuperclass()) {
            for (Method method : c.getDeclaredMethods()) {
                if (method.isAnnotationPresent(Test.class)) {
                    return true;
                }
            }
        }
        for (Class<?> nested : type.getDeclaredClasses()) {
            if (hasTests(nested)) {
                return true;
            }
        }
        return false;
    }
}
```

```java
package org.stuntgres.testing;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.fail;

import org.junit.jupiter.api.Test;
import org.stuntgres.pq.Feature;

@UnitTest
class GateConditionTest {
    @Test
    @Modes(Mode.UNIT)
    void runsInAListedMode() {
        assertEquals(Mode.UNIT, Mode.current());
    }

    @Test
    @Modes({Mode.ORACLE, Mode.NATIVE})
    void isSkippedInAnotherMode() {
        fail("a test gated to oracle and native mode ran in unit mode");
    }

    @Test
    @RequiresFeature(Feature.CLOSE_PREPARED)
    void aFeatureGateSkipsWithoutALibrary() {
        fail("a feature-gated test ran in unit mode, where no target library is loaded");
    }
}
```

`GateConditionTest` contributes two skips to every unit run from TEST-01G on: the
summary line of unit mode then ends with `skipped=2`.

### 10.8 Native mode (TEST-02)

Nothing is started: libspq runs in the test JVM. `tools/mode_native.sh` checks that
`build/libspq/lib/libspq.so` exists under the repository root (built by `make libspq`,
which LIBSPQ-BUILD-01-SKELETON provides) and points the loader's test seam at it with
prefix `SPQ`. Isolation: **a cluster per test class.** `NativeBackend.acquire()` returns
host `c<NNNNN>` (a counter), port 5432, database `test`, user `stuntgres`; libspq creates
cluster, database and role at the first connect (docs/LIBSPQ.md section 1).
`release()` calls `PqCluster.drop(host, "5432")`, which fails while a connection of the
cluster is open: a test class closes its connections, as in every mode. The cost per
class could not be measured (no libspq exists).

Both files are **not run**:

```sh
# mode_native.sh - native mode: this driver through libspq, the engine in-process.
# Nothing is started; the test JVM creates a cluster per test class and drops it.

mode_start() {
    local library=$REPO_ROOT/build/libspq/lib/libspq.so
    [ -f "$library" ] ||
        fail_start "$library does not exist; build it with 'make libspq' in the repository root"
    PROPS+=("-Dstuntgres.test.native.shim=$TARGET_DIR/native/libstuntgres_jni.so"
        "-Dstuntgres.test.native.library=$library" "-Dstuntgres.test.native.prefix=SPQ")
}

mode_stop() {
    return 0
}
```

```java
package org.stuntgres.testing;

import java.util.concurrent.atomic.AtomicInteger;
import org.stuntgres.pq.PqCluster;
import org.stuntgres.pq.PqException;

/**
 * Native mode: libspq in this process. A test class gets a cluster of its own (a host
 * name nobody else uses), which is dropped when the class ends.
 */
final class NativeBackend implements Backend {
    private static final String PORT = "5432";
    private final AtomicInteger counter = new AtomicInteger();

    NativeBackend() {
    }

    @Override
    public TestDatabase acquire() {
        String cluster = String.format("c%05d", counter.incrementAndGet());
        return new TestDatabase(Mode.NATIVE, cluster, Integer.parseInt(PORT), "test",
            "stuntgres", "stuntgres");
    }

    @Override
    public void release(TestDatabase database) throws PqException {
        PqCluster.drop(database.host(), PORT);
    }

    @Override
    public void close() {
        // Clusters are dropped one by one; nothing is left to stop.
    }
}
```

### 10.9 Stand-in libraries for unit tests (PQ-02B)

The loader and the load native method keep state for the whole process, and unit mode
has no real target library. Their tests therefore run small main classes in child JVMs
(`ChildJvm`) against stand-in libraries built from `src/test/c`:

- `fakepq.c` defines every member of the shim's function table under a chosen prefix as
  a function that returns 0, with weak linkage; another file of `src/test/c` can give a
  member a real body (`fake_cluster.c` of PQ-14A does for `Xversion` and `XclusterDrop`).
- `tools/build_test_natives.sh <arch> <sources>` compiles all sources of `src/test/c`
  into four libraries named `libspq.so` under `target/test-native/`: `full/` (all
  members, next to a copy of the shim: a complete directory for `stuntgres.native.dir`),
  `noopt/` (required members only), `broken/` (without `notifies` and `getCopyData`),
  `other/` (a second complete copy, another inode); and the class-path root
  `target/test-native/resources/` with `META-INF/native/linux-<arch>/` as a jar carries
  it (shim, `libspq.so`, `natives.sha256`). It rebuilds when a source, `sjni.h` or the
  shim is newer than its stamp. The Makefile runs it when `src/test/c` has files.

No test calls a database function of a stand-in.

`src/test/c/fakepq.c`:

```c
/*
 * fakepq.c - stand-in target library for the tests of the loader and of the load
 * native method. It defines the symbols of the shim's function table under the
 * prefix FAKE_PREFIX and nothing behind them: every function returns 0 unless
 * another file of src/test/c defines it (the definitions here are weak).
 *
 *   FAKE_PREFIX     symbol prefix, for example SPQ
 *   FAKE_OPTIONAL   define to add the optional functions
 *   FAKE_BROKEN     define to leave out two required functions (notifies, getCopyData)
 */
#include "sjni.h"

#define PASTE2(a, b) a##b
#define PASTE(a, b) PASTE2(a, b)
#define FAKE(name) PASTE(FAKE_PREFIX, name)
#define EXPORT __attribute__((visibility("default")))

#ifdef FAKE_BROKEN
#define notifies fake_left_out_1
#define getCopyData fake_left_out_2
#endif

/* Weak, so that another file of src/test/c can give a function a body of its own. */
#define X(name, ret, args) \
	EXPORT __attribute__((weak)) void *FAKE(name)(void); \
	void *FAKE(name)(void) { return (void *) 0; }
SJNI_REQUIRED_FUNCTIONS(X)
#ifdef FAKE_OPTIONAL
SJNI_OPTIONAL_FUNCTIONS(X)
#endif
#undef X
```

`tools/build_test_natives.sh`:

```sh
#!/bin/bash
# build_test_natives.sh ARCH SOURCE...: the stand-in libraries of the loader tests.
# Every file of src/test/c is compiled into each stand-in (fakepq.c gives every function
# of the shim's table a weak body that returns 0; other files replace single ones).
# Under target/test-native/ it builds:
#   full/libstuntgres_jni.so + full/libspq.so      a complete directory for stuntgres.native.dir
#   noopt/libspq.so                                required functions only
#   broken/libspq.so                               two required functions missing
#   other/libspq.so                                a second complete copy (another inode)
# and, as class-path root target/test-native/resources/, the same pair as the jar carries
# it: META-INF/native/linux-ARCH/{libstuntgres_jni.so,libspq.so,natives.sha256}.
set -eu
arch=$1
shift
out=target/test-native
cc=${CC:-cc}
flags="-std=c11 -Wall -Wextra -Werror -fPIC -fvisibility=hidden -shared -Isrc/main/c -I${JAVA_HOME}/include -I${JAVA_HOME}/include/linux"
src="$*"
stamp=$out/stamp
fresh=1
for file in $src src/main/c/sjni.h target/native/libstuntgres_jni.so; do
    if [ ! -f "$stamp" ] || [ "$file" -nt "$stamp" ]; then
        fresh=0
    fi
done
if [ "$fresh" = 1 ]; then
    exit 0
fi
mkdir -p "$out/full" "$out/noopt" "$out/broken" "$out/other" \
    "$out/resources/META-INF/native/linux-$arch"
$cc $flags -DFAKE_PREFIX=SPQ -DFAKE_OPTIONAL -o "$out/full/libspq.so" $src
$cc $flags -DFAKE_PREFIX=SPQ -DFAKE_OPTIONAL -DFAKE_OTHER -o "$out/other/libspq.so" $src
$cc $flags -DFAKE_PREFIX=SPQ -o "$out/noopt/libspq.so" $src
$cc $flags -DFAKE_PREFIX=SPQ -DFAKE_OPTIONAL -DFAKE_BROKEN -o "$out/broken/libspq.so" $src
cp target/native/libstuntgres_jni.so "$out/full/"
cp target/native/libstuntgres_jni.so "$out/full/libspq.so" "$out/resources/META-INF/native/linux-$arch/"
(cd "$out/resources/META-INF/native/linux-$arch" && sha256sum -- *.so >natives.sha256)
touch "$stamp"
```

`src/test/c/fake_cluster.c` (PQ-14A):

```c
/*
 * fake_cluster.c - stand-in bodies of the two extensions that need no connection,
 * for the unit tests of org.stuntgres.pq.PqCluster. Only the complete stand-in
 * (FAKE_OPTIONAL) has them.
 */
#include <stdio.h>
#include <string.h>

#include "sjni.h"

#define PASTE2(a, b) a##b
#define PASTE(a, b) PASTE2(a, b)
#define FAKE(name) PASTE(FAKE_PREFIX, name)
#define EXPORT __attribute__((visibility("default")))

#ifdef FAKE_OPTIONAL
EXPORT const char *FAKE(Xversion)(void);
EXPORT int	FAKE(XclusterDrop)(const char *host, const char *port, char *errbuf, int errbufsize);

const char *
FAKE(Xversion)(void)
{
	return "0.0.0-fake";
}

/* Succeeds for every cluster except "busy", which has open connections. */
int
FAKE(XclusterDrop)(const char *host, const char *port, char *errbuf, int errbufsize)
{
	if (strcmp(host, "busy") != 0)
		return 1;
	snprintf(errbuf, (size_t) errbufsize, "cluster \"%s\" (port %s) has open connections",
			 host, port);
	return 0;
}
#endif
```

### 10.10 Layout of `src/test` and rules for tests

```
src/test/java/org/stuntgres/testing/   support API, backends, their tests
src/test/java/org/stuntgres/pq/        tests of org.stuntgres.pq (same package: they may
                                       use package-private members), probe main classes
src/test/c/                            stand-in libraries
src/test/resources/                    nothing in this foundation
```

Rules, in addition to R7 of the implementation parts:

1. One test file per part, named in 11.3; a probe main class run by `ChildJvm` is its
   own file next to the test.
2. Expectations are literals. The literals of 11.3 were observed on PostgreSQL 18.6
   through libpq 18.6 (oracle mode) and, where a test runs in reference mode, through
   pgjdbc 42.7.13.
3. Never assert: the text of `server_version`; the fields `F`, `L`, `R`; a backend pid's
   value; a port; timings other than "at least the requested wait" and generous upper
   bounds given in the test list.
4. No sleeps. Wait with `Waits.until` or with a bounded blocking call of the driver.
   Never call `System.gc()` in a loop without a pause: back-to-back collections starve
   every other thread **(measured: a thread that had to connect got 15 ms of CPU in 13
   s)**; `Waits.until(..., () -> { System.gc(); return condition; })` is the pattern.
5. A cancel is repeated until the cancelled call has returned (a request that arrives
   before the statement started has no effect).
6. A connection the test opens is closed by the test (try-with-resources). Do not call
   `close()` explicitly on a try-with-resources variable (9.1, lint).
7. Output: a passing test prints nothing.
8. Tests that need a second process-wide state (loader, JVM exit) use `ChildJvm`; they
   pass the loader's test-seam properties of the parent on to the child when the child
   needs the real library.


## 11. Steps

### 11.1 Parts

Every part lists `JDBC-DESIGN-NATIVE` as a prerequisite; the table shows the others.
"Size" is code + tests in lines. IDs are prefixed `JDBC-`.

| Part | Outcome | Prerequisites | Size |
|---|---|---|---|
| `BUILD-01-SKELETON` | Directory, `pom.xml`, `Makefile`, `.gitignore`, `README.md`, package-info, version resource, the three build scripts | – | 360 + 0 |
| `BUILD-02-RUNNER` | `tools/run_mode.sh`, `UnitTest`, `Mode`, `Waits`, `ChildJvm`; `make check MODE=unit` | BUILD-01 | 285 + 110 |
| `PQ-01A-CONSTANTS` | `ConnStatus`, `ExecStatus`, `TransactionStatus`, `DiagField`, `Feature` | BUILD-02 | 215 + 80 |
| `PQ-01B-VALUES` | `PqException`, `PqText`, `Conninfo`, `PqNotice`, `PqNotification` | BUILD-02 | 285 + 130 |
| `PQ-02A-HEADER` | `sjni.h`, `sjni_check.c`; the shim builds | PQ-01A, PQ-01B | 385 + 0 |
| `PQ-02B-UTIL` | `sjni_util.c`, stand-in library sources and their build script | PQ-02A | 210 + 0 |
| `PQ-02C-LOAD` | `sjni_load.c`, `PqLibrary` | PQ-02B | 390 + 0 |
| `PQ-03A-LOADER` | `NativeLoader` (platform, test seam, directory property), stub `NativeExtractor` | PQ-02C | 240 + 150 |
| `PQ-03B-EXTRACT` | `NativeExtractor`: extraction from the class path | PQ-03A | 205 + 110 |
| `TEST-01A-SUPPORT` | `DatabaseTest`, `ReferenceToo`, `NativeOnly`, `DatabaseExtension`, `Backend`, `Props`, `TestDatabase` | PQ-01B | 345 + 70 |
| `TEST-01G-GATES` | `Modes`, `RequiresFeature`, `GateCondition`, tag audit | PQ-03A, TEST-01A | 90 + 95 |
| `TEST-01B-CONTAINER` | Reference and oracle mode: scripts, libpq 18, `ContainerBackend` | TEST-01G | 195 + 110 |
| `TEST-01C-SERVER` | Server mode: script, `ServerBackend` | TEST-01B | 165 + 90 |
| `PQ-04A-CONN-BASE` | `sjni_conn.c`, `ConnBase`, stubs `HandleRelease` and `sjni_notice.c` | TEST-01C | 310 + 110 |
| `PQ-04B-SESSION` | `sjni_session.c`, `ConnSession`, `PqConnection`, seven stub classes | PQ-04A | 225 + 110 |
| `PQ-05A-RESULT-CLASS` | `PqResult` | PQ-01A, PQ-01B | 326 + 90 |
| `PQ-05B-RESULT-COPY` | `sjni_result.c`, `ResultCopy` | PQ-05A, PQ-04B | 270 + 150 |
| `PQ-06A-EXEC` | `sjni_exec.c`, `ConnExec` | PQ-05B | 130 + 170 |
| `PQ-06B-PARAMS` | `sjni_params.c`, `ConnParams` | PQ-06A | 180 + 160 |
| `PQ-07-PREPARED` | `sjni_prepared.c`, `ConnPrepared` | PQ-06B | 205 + 170 |
| `PQ-08-NOTICES` | `sjni_notice.c`, `ConnNotices` | PQ-06A | 135 + 120 |
| `PQ-09-CANCEL` | `sjni_cancel.c`, `PqCancel` | PQ-06A | 125 + 130 |
| `PQ-10-NOTIFY` | `sjni_notify.c`, `ConnNotify` | PQ-06A | 165 + 120 |
| `PQ-11-COPY` | `sjni_copy.c`, `ConnCopy` | PQ-06A | 145 + 150 |
| `PQ-12-ESCAPE` | `sjni_escape.c`, `ConnEscape` | PQ-04B | 145 + 110 |
| `PQ-13-CLEANER` | `HandleRelease` with a `Cleaner` | PQ-06A | 25 + 160 |
| `PQ-14A-CLUSTER` | `sjni_cluster.c`, `PqCluster`, `fake_cluster.c` | PQ-03A | 125 + 70 |
| `TEST-02-NATIVE` | Native mode: `mode_native.sh`, `NativeBackend` | TEST-01A, PQ-04B, PQ-14A, LIBSPQ-CABI-03-BLOCKING, LIBSPQ-CABI-14-CONTROL | 50 + 80 |
| `PQ-14B-EXT` | `sjni_ext.c`, `PqExt`; native mode only | PQ-06A, TEST-02, LIBSPQ-CABI-15-DUMP | 125 + 110 |

Changes against the list of the part record, all made for the size limit and for
one-owner-per-file: BUILD-01 split in two (the build files; the runner and its support
classes); PQ-01 split in two (constants; value classes); PQ-02 split in three (header and
constant checks; helpers and stand-in sources; load); PQ-03 split in two (loader;
extraction); TEST-01 split in four (support API; gates; container modes; server mode);
PQ-04 split in two; PQ-05 split in two (the pure Java class, testable in unit mode; the
copy); PQ-06 split in two (simple; parameterized); PQ-14 split in two with native mode
between them, because the cluster drop that native mode's isolation needs is itself an
extension wrapper: PQ-14A is unit-tested against a stand-in library and needs no libspq.
No part exceeds about 420 lines; none needs a stronger model. LIBSPQ prerequisites appear
only on TEST-02 and PQ-14B (plan J4, B15).

Parallelism: after BUILD-02 the two PQ-01 parts run in parallel; PQ-05A can start as soon
as they are done; after PQ-06A the parts PQ-06B, PQ-08, PQ-09, PQ-10, PQ-11 and PQ-13 run
in parallel, PQ-12 already after PQ-04B.

### 11.2 Stub files

A stub is created by one part so that the code compiles and links, and is replaced as a
whole file by the part that owns it. A part that replaces a stub edits no other stub.

Created by PQ-03A, replaced by PQ-03B (`src/main/java/org/stuntgres/pq/NativeExtractor.java`):

```java
package org.stuntgres.pq;

import java.nio.file.Path;

/** Stub: no extraction yet. JDBC-PQ-03B-EXTRACT replaces this file. */
final class NativeExtractor {
    private NativeExtractor() {
    }

    /**
     * Extracts the native files of the class path.
     *
     * @param arch x86_64 or aarch64
     * @return the directory that holds them
     * @throws PqException kind LOAD_FAILED: extraction is not available
     */
    static Path extract(String arch) throws PqException {
        throw NativeLoader.failed("no native files for linux-" + arch + " on the class path"
            + " (META-INF/native/linux-" + arch + "/" + NativeLoader.INDEX_NAME + ") and "
            + NativeLoader.DIR_PROPERTY + " is not set");
    }
}
```

Created by PQ-04A, replaced by PQ-13 (`src/main/java/org/stuntgres/pq/HandleRelease.java`):

```java
package org.stuntgres.pq;

/** Runs the release action of a native handle once. JDBC-PQ-13-CLEANER adds collection. */
final class HandleRelease {
    private HandleRelease() {
    }

    /**
     * Registers a release action.
     *
     * @param owner the object that owns the handle
     * @param action releases the handle; must not refer to {@code owner}
     * @return a runnable that runs {@code action} now unless it already ran
     */
    static Runnable register(Object owner, Runnable action) {
        return action;
    }
}
```

Created by PQ-04A, replaced by PQ-08 (`src/main/c/sjni_notice.c`):

```c
/* sjni_notice.c - stub: notices are dropped. JDBC-PQ-08-NOTICES replaces this file. */
#include "sjni.h"

static void
receive(void *arg, const sjni_pgresult *res)
{
	(void) arg;
	(void) res;
}

void
sjni_notice_install(sjni_conn *conn)
{
	sjni_pq.setNoticeReceiver(conn->pg, receive, conn);
}

void
sjni_notice_discard(sjni_conn *conn)
{
	(void) conn;
}
```

Created by PQ-04B, each replaced by the part named in its comment
(`src/main/java/org/stuntgres/pq/ConnNotices.java`, `ConnExec.java`, `ConnParams.java`,
`ConnPrepared.java`, `ConnNotify.java`, `ConnCopy.java`, `ConnEscape.java`):

```java
package org.stuntgres.pq;

/** Notices the server sent; implemented by JDBC-PQ-08-NOTICES. */
public abstract class ConnNotices extends ConnSession {
    ConnNotices(long handle) {
        super(handle);
    }
}
```

```java
package org.stuntgres.pq;

/** Simple execution; implemented by JDBC-PQ-06A-EXEC. */
public abstract class ConnExec extends ConnNotices {
    ConnExec(long handle) {
        super(handle);
    }
}
```

```java
package org.stuntgres.pq;

/** Parameterized execution; implemented by JDBC-PQ-06B-PARAMS. */
public abstract class ConnParams extends ConnExec {
    ConnParams(long handle) {
        super(handle);
    }
}
```

```java
package org.stuntgres.pq;

/** Prepared statements; implemented by JDBC-PQ-07-PREPARED. */
public abstract class ConnPrepared extends ConnParams {
    ConnPrepared(long handle) {
        super(handle);
    }
}
```

```java
package org.stuntgres.pq;

/** Notifications; implemented by JDBC-PQ-10-NOTIFY. */
public abstract class ConnNotify extends ConnPrepared {
    ConnNotify(long handle) {
        super(handle);
    }
}
```

```java
package org.stuntgres.pq;

/** The COPY sub-protocol; implemented by JDBC-PQ-11-COPY. */
public abstract class ConnCopy extends ConnNotify {
    ConnCopy(long handle) {
        super(handle);
    }
}
```

```java
package org.stuntgres.pq;

/** Escaping and the client encoding; implemented by JDBC-PQ-12-ESCAPE. */
public abstract class ConnEscape extends ConnCopy {
    ConnEscape(long handle) {
        super(handle);
    }
}
```

**(measured)** The tree in the state after PQ-04B (all nine stubs above except
`NativeExtractor`, with `ConnBase`, `ConnSession` and `PqConnection`, and none of the
later files) compiles with `-Xlint:all -Werror`, links with `--no-undefined` and passes
`check_jni.sh`; so do the states after BUILD-01, PQ-02A and PQ-02B. The final files that
replace the stubs ran in the validation slice.

### 11.3 What each part does

Conventions for all parts:

- "Write `<file>` as in section n" means: the file is the code block of that section,
  character for character. Do not restructure it.
- Tests are written first and seen to fail (a compile error because the class does not
  exist yet counts). Test classes are in the package named by their path, carry the tag
  annotation given, and follow 10.10. `db` is the `TestDatabase` parameter; `conn` is a
  `PqConnection` (or, before PQ-04B, the object the part names) opened by the test with
  `db.conninfo()` and closed by it; `utf8("x")` means `"x".getBytes(UTF_8)`.
- "status X" means `result.status() == ExecStatus.X`.
- A literal in backticks that continues on the next line of this document has exactly
  one space at the break. Messages the library formats have two spaces after `ERROR:`,
  `NOTICE:`, `WARNING:`, `DETAIL:`, `HINT:`, `CONTEXT:` and `FATAL:` (`ERROR:  division by
  zero`); this document never breaks a line at that place.
- Commands are run from the repository root: `make -C bindings/jdbc check MODE=<mode>
  TEST=<Class>`.
- Modes that do not exist yet: until TEST-01B is merged `MODE=reference` and
  `MODE=oracle` end with `run_mode: cannot start <mode> mode: tools/mode_<mode>.sh does
  not exist yet` (exit status 3 of the script), and `MODE=server` does until TEST-01C is
  merged. For a part that is merged before them that outcome satisfies the rule "run
  every mode"; the commit body records it.

#### JDBC-BUILD-01-SKELETON

Files (under `bindings/jdbc`): `README.md`, `.gitignore`, `pom.xml`, `Makefile`,
`tools/check_jdk.sh`, `tools/check_jni.sh`, `tools/stage_natives.sh`,
`src/main/java/org/stuntgres/pq/package-info.java`,
`src/main/resources-filtered/org/stuntgres/version.properties`. Design sections: 1.2,
3.1, 3.2.

Step.

1. Create the directories. `.gitignore` is the single line `/target/`.
2. Write `pom.xml` as in 3.1, `Makefile` as in 3.2 (recipe lines start with a tab), the
   three scripts as in 3.2; make the scripts executable (`chmod +x`, and
   `git update-index --chmod=+x` after adding them).
3. `version.properties` is the single line `version=${project.version}`.
4. `package-info.java`:
   ```java
   /**
    * Java face of the native shim: the only package that declares native methods.
    * Nothing here is consumer API.
    */
   package org.stuntgres.pq;
   ```
5. `README.md`:

````markdown
# stuntgres JDBC driver

`bindings/jdbc` is the JDBC driver of stuntgres: Maven artifact
`org.stuntgres:stuntgres-jdbc`, Java 17, no runtime dependencies. It runs the engine
inside the JVM through the C library libspq ([docs/LIBSPQ.md](../../docs/LIBSPQ.md))
and a small JNI shim.

**Status.** The driver is being built by the `JDBC-*` catalog parts. The design of
this directory is [docs/design/JDBC-NATIVE.md](../../docs/design/JDBC-NATIVE.md).

## Layout

| Path | Content |
|---|---|
| `pom.xml`, `Makefile` | The build. Always go through the Makefile. |
| `src/main/java/org/stuntgres/pq` | Java face of the shim; the only package with native methods. |
| `src/main/c` | The JNI shim `libstuntgres_jni.so`: one C file per function family, one private header. |
| `src/test/java`, `src/test/c` | JUnit 5 tests and the stand-in libraries of the loader tests. |
| `tools` | Scripts the Makefile runs: JDK check, JNI check, test modes. |
| `target` | All build output; ignored by Git. |

## Testing

```sh
make -C bindings/jdbc check MODE=<unit|reference|oracle|server|native> [TEST=<selector>]
```

`MODE` defaults to `unit`. `reference` and `oracle` need Docker, `server` needs Go and
(once) Docker, `native` needs `build/libspq/lib/libspq.so`. `TEST` is a Surefire
selector: a class (`SkeletonTest`), a method (`SkeletonTest#modeIsPassed`) or a pattern
(`Pq*Test`). The JDK is `JAVA_HOME`, or the JDK of the `java` on `PATH`; it must be 17
or newer. The last line of a run is
`stuntgres-jdbc: mode=<mode> tests=<n> failures=<n> errors=<n> skipped=<n>`.
````

This part has no JUnit test: `make check` needs `tools/run_mode.sh` of BUILD-02. It is
checked by commands (run in `bindings/jdbc`):

- `make compile` exits 0 and creates `target/classes/org/stuntgres/version.properties`
  with the line `version=0.1.0` and the directory `target/jni`.
- `make shim` exits 0 and builds nothing (there is no C file yet).
- `make jar` exits 0; `unzip -l target/stuntgres-jdbc-0.1.0.jar` lists
  `org/stuntgres/version.properties` and no `META-INF/native` entry; `unzip -p
  target/stuntgres-jdbc-0.1.0.jar META-INF/MANIFEST.MF` contains
  `Automatic-Module-Name: org.stuntgres.jdbc` and `Implementation-Version: 0.1.0`.
- `JAVA_HOME=/nonexistent make compile` prints `bindings/jdbc: no JDK found: set
  JAVA_HOME to a JDK 17 or newer (JAVA_HOME='/nonexistent')` and fails. When a JDK older
  than 17 is installed (the design host has `~/.jdks/temurin-11.0.29`), `JAVA_HOME=<it>
  make compile` prints `bindings/jdbc: JDK 17 or newer is required; <it> is version
  11.0.29` and fails.
- `make clean` removes `target`; `git status --short` then shows only the files of this
  part.
- From the repository root: `go build ./...` succeeds and `go list ./... | grep -c
  bindings/jdbc` prints 0.

#### JDBC-BUILD-02-RUNNER

Files: `tools/run_mode.sh`, `src/test/java/org/stuntgres/testing/UnitTest.java`,
`Mode.java`, `Waits.java`, `ChildJvm.java`, `SkeletonTest.java`, `WaitsTest.java`,
`ChildJvmTest.java`, `EchoProbe.java`. Design sections: 10.1, 10.6 (the four files of
BUILD-02), 10.10.

Step. Write `tools/run_mode.sh` as in 10.1 (executable) and the four support classes as
in 10.6.

Tests to write first (all `@UnitTest`, package `org.stuntgres.testing`):

`SkeletonTest`:
- `versionResourceIsFiltered`: the resource `/org/stuntgres/version.properties`, loaded
  as `java.util.Properties`, has `version` = `0.1.0`.
- `modeIsPassed`: `System.getProperty("stuntgres.test.mode")` is `unit` and
  `Mode.current() == Mode.UNIT`.
- `testsRunOnTheJdkTheMakefileChose`: `Paths.get(System.getProperty("stuntgres.jdk.home")).toRealPath()`
  equals `Paths.get(System.getProperty("java.home")).toRealPath()`, and
  `Runtime.version().feature() >= 17`.
- `buildDirectoryIsPassed`: `stuntgres.test.builddir` ends with `/bindings/jdbc/target`
  and `stuntgres.test.reporoot` is a directory that contains `go.mod`.

`WaitsTest`:
- `returnsWhenTheConditionHolds`: `Waits.until(1000, "x", () -> true)` returns.
- `pollsUntilTheConditionHolds`: a counter-based condition that is true on its fifth
  call: `Waits.until(5000, "five calls", ...)` returns and the counter is 5.
- `failsWithTheNameOfTheCondition`: `Waits.until(50, "the flag", () -> false)` throws
  `AssertionError` with the message `timed out after 50 ms waiting for the flag`.

`ChildJvmTest` with the main class `EchoProbe` (prints `prop=` +
`System.getProperty("echo.value")`, then each argument on its own line (for the argument
`pid` the pid of its JVM instead) to standard output, then `err` to standard error, and
ends with `System.exit(3)`):
- `runsAMainClassInAFreshJvm`: `ChildJvm.run(EchoProbe.class, Map.of("echo.value",
  "v1"), List.of(), "a", "b c")` has exit code 3 and output `prop=v1\na\nb c\nerr\n`.
- `childIsAnotherProcess`: `ChildJvm.run(EchoProbe.class, Map.of(), List.of(), "pid")`: the
  second output line, parsed as a number, differs from `ProcessHandle.current().pid()` of
  the test JVM.

Acceptance commands: `make -C bindings/jdbc check MODE=unit` prints
`stuntgres-jdbc: mode=unit tests=9 failures=0 errors=0 skipped=0` and exits 0;
`MODE=bogus` exits non-zero with the usage line of `run_mode.sh`; `MODE=oracle` prints
`run_mode: cannot start oracle mode: tools/mode_oracle.sh does not exist yet`;
`MODE=unit TEST=NoSuchTest` fails (Surefire: no tests matching the pattern).

#### JDBC-PQ-01A-CONSTANTS

Files: `src/main/java/org/stuntgres/pq/ConnStatus.java`, `ExecStatus.java`,
`TransactionStatus.java`, `DiagField.java`, `Feature.java`,
`src/test/java/org/stuntgres/pq/ConstantsTest.java`. Design sections: 9.2.

Step. Write the five classes as in 9.2.

Tests to write first (`ConstantsTest`, `@UnitTest`): one test method per class.
- `connStatus`: `OK` 0, `BAD` 1; `name(0)` = `CONNECTION_OK`, `name(1)` =
  `CONNECTION_BAD`, `name(7)` = `CONNECTION_7`.
- `execStatus`: the thirteen values 0 to 12 in the order of 9.2; `name(2)` =
  `PGRES_TUPLES_OK`, `name(7)` = `PGRES_FATAL_ERROR`, `name(12)` = `PGRES_TUPLES_CHUNK`,
  `name(13)` = `PGRES_13`, `name(-1)` = `PGRES_-1`.
- `transactionStatus`: values 0 to 4; `name(0)` = `PQTRANS_IDLE`, `name(3)` =
  `PQTRANS_INERROR`, `name(9)` = `PQTRANS_9`.
- `diagField`: the string built from the eighteen constants cast to `char` in the order
  of 9.2 equals `SVCMDHPpqWstcdnFLR`.
- `feature`: values 0 to 9 and `COUNT` 10; `name(0)` = `closePrepared`, `name(4)` =
  `setChunkedRowsMode`, `name(5)` = `Xversion`, `name(9)` = `Xrestore`.
- `headersAreGenerated`: after `make compile`, the files
  `target/jni/org_stuntgres_pq_ExecStatus.h` and `..._Feature.h` exist (path from
  `stuntgres.test.builddir`) and the first contains the line `#define
  org_stuntgres_pq_ExecStatus_TUPLES_OK 2L`.

#### JDBC-PQ-01B-VALUES

Files: `src/main/java/org/stuntgres/pq/PqException.java`, `PqText.java`,
`Conninfo.java`, `PqNotice.java`, `PqNotification.java`,
`src/test/java/org/stuntgres/pq/ValuesTest.java`. Design sections: 9.1 (Text,
Messages), 9.3.

Step. Write the five classes as in 9.3.

Tests to write first (`ValuesTest`, `@UnitTest`, same package, so the package-private
constructors and `PqText` are reachable):
- `exceptionKinds`: the ten constants have the values 1 to 10 in the order of 9.3;
  `new PqException(PqException.CLOSED, "m")` has `kind()` 5 and message `m`; the
  constructor with a cause keeps the cause; `new PqException(8, utf8("zażółć"))` has the
  message `zażółć`.
- `cstring`: `PqText.cstring("", "x")` has length 0; `cstring("zaż 😀",
  "x")` equals the bytes `7a 61 c5 bc 20 f0 9f 98 80`; `cstring("a\0b", "the statement")`
  throws kind 6 with message `the statement contains a NUL character`; `cstring("lone
  \uD800 surrogate", "x")` equals `utf8("lone ? surrogate")`.
- `checkNoNul`: passes for `{1, 2}`; throws kind 6 `parameter $2 contains a NUL
  character` for `{1, 0}` with that name.
- `decode`: `decode(utf8("abc"), 1, 2)` = `bc`; a zero length gives the empty string;
  `decode(new byte[] {'c', 'a', 'f', (byte) 0xe9}, 0, 4)` = `caf�`.
- `field`: with `packed` = `S` `ERROR` 0 `C` `22012` 0 `M` `division by zero` 0 (bytes):
  `field(packed, 'C')` = `22012`, `field(packed, 'M')` = `division by zero`,
  `field(packed, 'H')` is null; `field(new byte[0], 'C')` is null.
- `trimNewlines`: `"a\n\n"` → `a`; `"a\nb\n"` → `"a\nb"`; `""` → `""`.
- `conninfo`: `new Conninfo().add("host", "h").add("dbname", "it's").add("password",
  "a\\b").add("options", "").toString()` equals
  `host='h' dbname='it\'s' password='a\\b' options=''` (the value characters `'` and `\`
  each preceded by one backslash).
- `notice`: `new PqNotice(packed, utf8("NOTICE:  hello 42\n"))` with packed fields `S`
  `NOTICE`, `C` `00000`, `M` `hello 42`: `message()` and `toString()` = `NOTICE:  hello
  42`, `field('C')` = `00000`, `field('D')` null.
- `notification`: `new PqNotification(7, utf8("ch"), utf8(""))`: `backendPid()` 7,
  `channel()` `ch`, `payload()` empty, `toString()` = `PqNotification[7, ch, ]`.

#### JDBC-PQ-02A-HEADER

Files: `src/main/c/sjni.h`, `src/main/c/sjni_check.c`. Design sections: 4 (conventions),
4.1, 4.3 (`sjni_check.c`), 3.2 (keeping C and Java in step).

Step. Write both files as in 4.1 and 4.3. `make shim` now compiles `sjni_check.c` into
`target/native/libstuntgres_jni.so` (a library without functions) and `check_jni.sh`
passes (no native method is declared yet).

No JUnit test. Checks (in `bindings/jdbc`): `make shim` exits 0 and creates
`target/native/libstuntgres_jni.so`; `nm -D --defined-only
target/native/libstuntgres_jni.so | grep -c ' T '` prints 0; temporarily changing
`TUPLES_OK = 2` to `3` in `ExecStatus.java` makes `make shim` fail with a static
assertion that names `org_stuntgres_pq_ExecStatus_TUPLES_OK` (undo the change);
`grep -c 'X(' src/main/c/sjni.h` is at least 80; `make check MODE=unit` still passes.

#### JDBC-PQ-02B-UTIL

Files: `src/main/c/sjni_util.c`, `src/test/c/fakepq.c`, `tools/build_test_natives.sh`.
Design sections: 4.3 (`sjni_util.c`), 10.9.

Step. Write the three files as in 4.3 and 10.9 (the script executable). `make
test-natives` then builds the stand-in libraries.

No JUnit test (the first users are PQ-02C and PQ-03A). Checks: `make shim` exits 0;
`make test-natives` creates `target/test-native/full/libspq.so`,
`full/libstuntgres_jni.so`, `noopt/libspq.so`, `broken/libspq.so`, `other/libspq.so` and
`resources/META-INF/native/linux-<arch>/natives.sha256`; `nm -D --defined-only
target/test-native/full/libspq.so | grep -c ' W SPQ'` prints 62, for `noopt` 52 (62 and 52
members of the function table, all weak);
`nm -D --defined-only target/test-native/broken/libspq.so | grep -c -E
'SPQ(notifies|getCopyData)$'` prints 0; a second `make test-natives` compiles nothing;
`make check MODE=unit` still passes.

#### JDBC-PQ-02C-LOAD

Files: `src/main/c/sjni_load.c`, `src/main/java/org/stuntgres/pq/PqLibrary.java`.
Design sections: 4.2, 9.4, 5.1 rows 1 to 3.

Step. Write both files as in 4.2 and 9.4.

No JUnit test of its own: only `NativeLoader` may call `System.load` (rule R9), so the
load is first exercised by the tests of PQ-03A, which is the next part. Checks: `make
shim` exits 0 and `check_jni.sh` passes; `nm -D --defined-only
target/native/libstuntgres_jni.so | awk '{print $3}'` prints exactly
`Java_org_stuntgres_pq_PqLibrary_features0`, `..._libVersion0`, `..._load0` and
`JNI_OnLoad`; `ldd target/native/libstuntgres_jni.so` lists only `linux-vdso`, `libc.so.6`
and the dynamic linker; `make check MODE=unit` still passes.

#### JDBC-PQ-03A-LOADER

Files: `src/main/java/org/stuntgres/pq/NativeLoader.java`,
`src/main/java/org/stuntgres/pq/NativeExtractor.java` (the stub of 11.2),
`src/test/java/org/stuntgres/pq/LoaderProbe.java`,
`src/test/java/org/stuntgres/pq/NativeLoaderTest.java`. Design sections: 8.1, 8.2, 4.2
(measured behavior), 10.9.

Step. Write `NativeLoader.java` as in 8.2 and the stub as in 11.2.

`LoaderProbe` (public final class with `main`): for each argument in order: an argument
that contains `=` sets that system property; `maps` prints `mapped shims=<a>
targets=<b>` where `a` counts the lines of `/proc/self/maps` that contain ` r-xp ` and
`libstuntgres_jni`, and `b` those that contain ` r-xp ` and end with `/libspq.so`; `load`
calls `NativeLoader.ensureLoaded()` and prints `loaded prefix=<prefix> features=<ten
characters 0 or 1, feature 0 first> file=<file name of PqLibrary.path()>`, or, on a
`PqException`, `failed kind=<kind> message=<message>`.

Tests to write first (`NativeLoaderTest`, `@UnitTest`). `N` is
`<stuntgres.test.builddir>/test-native`; `run(properties, classPath, steps...)` calls
`ChildJvm.run(LoaderProbe.class, ...)`, asserts exit code 0 and returns the output;
`ALL` is `loaded prefix=SPQ features=1111111111 file=libspq.so\n`.
- `nothingIsMappedBeforeTheFirstLoad`: property `stuntgres.native.dir` = `N/full`, steps
  `maps load maps`: output `mapped shims=0 targets=0\n` + `ALL` + `mapped shims=1
  targets=1\n`.
- `directoryProperty`: the same property, steps `load load`: `ALL` + `ALL`.
- `directoryPropertyMustBeAbsoluteAndComplete`: `relative/dir` → `failed kind=2
  message=cannot load the stuntgres native libraries: stuntgres.native.dir=relative/dir:
  the path is not absolute\n`; `N/noopt` → `... stuntgres.native.dir=<N>/noopt:
  libstuntgres_jni.so not found\n`.
- `optionalFunctionsBecomeFeatureFlags`: test seam with shim `N/full/libstuntgres_jni.so`
  and library `N/noopt/libspq.so`: `loaded prefix=SPQ features=0000000000
  file=libspq.so\n`.
- `missingRequiredFunctionsAreNamedAndALaterLoadSucceedsAndStays`: seam with library
  `N/broken/libspq.so`, steps `load`, `stuntgres.test.native.library=<N>/full/libspq.so`,
  `load`, `stuntgres.test.native.library=<N>/other/libspq.so`, `load`: output `failed
  kind=2 message=cannot use <N>/broken/libspq.so: 2 required symbols are missing:
  SPQgetCopyData, SPQnotifies\n` + `ALL` + `ALL`.
- `testSeamNeedsALibrary`: seam shim set, library property absent: `failed kind=2
  message=cannot load the stuntgres native libraries: stuntgres.test.native.library is
  not set\n`.
- `missingFile`: seam with library `/nonexistent/libspq.so`: `failed kind=2
  message=cannot load /nonexistent/libspq.so: no such file\n`.
- `noNativeFilesAnywhere`: no property: `failed kind=2 message=cannot load the stuntgres
  native libraries: no native files for linux-<arch> on the class path
  (META-INF/native/linux-<arch>/natives.sha256) and stuntgres.native.dir is not set\n`
  (`<arch>` is `x86_64` when `os.arch` is `amd64`, else `os.arch`).
- `unsupportedPlatforms`: properties `os.name` = `Mac OS X`, `os.arch` = `aarch64`:
  `failed kind=3 message=stuntgres supports Linux with glibc on x86_64 and aarch64 only;
  this JVM runs on Mac OS X aarch64\n`; `os.name` = `Linux`, `os.arch` = `riscv64`: the
  same text ending `on Linux riscv64\n`.
- `muslIsRecognisedInAMemoryMap` (no child JVM): a file with the lines `7f00-7f10 r-xp
  00000000 fd:01 1 /lib/ld-musl-x86_64.so.1` and `7f20-7f30 r-xp 00000000 fd:01 2
  /usr/lib/jvm/lib/server/libjvm.so` gives `NativeLoader.usesMusl(file)` true; a file
  with `.../libc.so.6` and `/home/musl/libjvm.so` gives false; a missing file gives
  false.

#### JDBC-PQ-03B-EXTRACT

Files: `src/main/java/org/stuntgres/pq/NativeExtractor.java` (replaces the stub),
`src/test/java/org/stuntgres/pq/NativeExtractorTest.java`. Design sections: 8.3.

Step. Replace the stub with the file of 8.3.

Tests to write first (`NativeExtractorTest`, `@UnitTest`; `run`, `N`, `ALL` and
`LoaderProbe` as in PQ-03A; `R` = `N/resources`; `@TempDir Path cache`; property
`stuntgres.native.cache` = `cache`):
- `resourcesAreExtractedOnceAndReused`: class path `[R]`, step `load`: `ALL`. Then:
  `cache` contains exactly one directory, its name has 32 characters; `libspq.so` in it
  has the permissions `r-x------`; `cache` has `rwx------`; the inode
  (`Files.getAttribute(file, "unix:ino")`) of `libspq.so` is the same after a second run
  that prints `ALL`; the directory holds exactly two files.
- `concurrentJvmsExtractTheSameFiles`: eight `run` calls at once (a thread pool of 8),
  each prints `ALL`; afterwards the version directory holds exactly two files.
- `corruptResourceIsRejected`: a temporary class-path root with
  `META-INF/native/linux-<arch>/` holding copies of `natives.sha256` and
  `libstuntgres_jni.so` from `R` and a file `libspq.so` with the bytes `not a library`:
  `failed kind=2 message=cannot load the stuntgres native libraries:
  META-INF/native/linux-<arch>/libspq.so does not match its checksum\n`.
- `incompleteIndexIsRejected`: the same root with a `natives.sha256` that has only the
  line of `libstuntgres_jni.so`: `... natives.sha256 does not list libspq.so\n`.
- `cacheMustBePrivate`: `cache` given the permissions `rwxrwxr-x` before the run:
  the output starts with `failed kind=2 message=cannot load the stuntgres native
  libraries: no directory to extract them to (` and contains ` is not a private directory
  of this user`.
- `indexFormat` (no child JVM): `NativeExtractor.readIndex` of the three lines
  `30e5b1e470772750ba7515bb7bbdea1ac5f271eaa9aee84ac6e08f557ed215bd  libspq.so`,
  `b9d00de3356b6c15422e86219a853de46ffc31aa67e2ed7f374efb9cd2f66588 *libstuntgres_jni.so`
  and `garbage` returns a map of size 2 with those two digests under `libspq.so` and
  `libstuntgres_jni.so`.
- The test `noNativeFilesAnywhere` of PQ-03A must still pass with the same text.


#### JDBC-TEST-01A-SUPPORT

Files (under `src/test/java/org/stuntgres/testing/`): `DatabaseTest.java`,
`ReferenceToo.java`, `NativeOnly.java`, `DatabaseExtension.java`, `Backend.java`,
`Props.java`, `TestDatabase.java`, `TestDatabaseTest.java`. Design sections: 10.6, 10.2.

Step. Write the seven support files as in 10.6.

Tests to write first (`TestDatabaseTest`, `@UnitTest`, same package; it builds
`TestDatabase` objects with the package-private constructor):
- `conninfo`: `new TestDatabase(Mode.ORACLE, "127.0.0.1", 5433, "t00001", "u", "p'w")
  .conninfo()` equals
  `host='127.0.0.1' port='5433' dbname='t00001' user='u' password='p\'w' sslmode='disable'`.
- `conninfoIsNotAvailableInReferenceMode`: with `Mode.REFERENCE`, `conninfo()` throws
  `IllegalStateException` with the message `no libpq connection in reference mode`.
- `jdbcUrl`: `Mode.REFERENCE` → `jdbc:postgresql://127.0.0.1:5433/t00001`;
  `Mode.ORACLE`, `Mode.SERVER` → `jdbc:stuntgres://127.0.0.1:5433/t00001`;
  `new TestDatabase(Mode.NATIVE, "c00001", 5432, "test", "stuntgres", "stuntgres")
  .jdbcUrl()` → `jdbc:stuntgres://c00001:5432/test`.
- `accessors`: `mode()`, `host()`, `port()`, `name()`, `user()`, `password()` return the
  constructor arguments.
- `requiredProperty`: `Props.require("stuntgres.test.no.such.property")` throws
  `IllegalStateException` whose message starts with `system property
  stuntgres.test.no.such.property is not set`; `Props.require("stuntgres.test.mode")`
  returns `unit`.

The extension itself is exercised by the database tests of TEST-01B and TEST-01C; in
unit mode nothing uses it.

#### JDBC-TEST-01G-GATES

Files (under `src/test/java/org/stuntgres/testing/`): `Modes.java`,
`RequiresFeature.java`, `GateCondition.java`, `GateConditionTest.java`,
`TagAuditTest.java`. Design sections: 10.7.

Step. Write the five files as in 10.7 (the two tests are given completely).

Expected: `make -C bindings/jdbc check MODE=unit TEST=GateConditionTest` prints
`tests=3 failures=0 errors=0 skipped=2`; `TEST=TagAuditTest` prints `tests=1
failures=0`; a whole unit run has `skipped=2`. To see the audit fail: remove `@UnitTest`
from `SkeletonTest` temporarily; `TagAuditTest` fails and names
`org.stuntgres.testing.SkeletonTest` (undo it).

#### JDBC-TEST-01B-CONTAINER

Files: `tools/oracle-image.txt`, `tools/oracle_container.sh`, `tools/libpq18.sh`,
`tools/mode_reference.sh`, `tools/mode_oracle.sh`,
`src/test/java/org/stuntgres/testing/ContainerBackend.java`,
`src/test/java/org/stuntgres/testing/ImageTest.java`,
`src/test/java/org/stuntgres/testing/ContainerModeTest.java`. Design sections: 10.3,
10.4, 10.2.

Step. Write the six files as in 10.3 and 10.4. The four `.sh` files are sourced, not
executed; they need no executable bit.

Tests to write first:

`ImageTest` (`@UnitTest`):
- `imageMatchesTheGoFixture`: the first line of
  `<stuntgres.test.basedir>/tools/oracle-image.txt` equals the string between the quotes
  of the line that starts with `const referenceImage = ` in
  `<stuntgres.test.reporoot>/reference/fixture/fixture.go`, and equals
  `postgres:18.6-bookworm@sha256:afc7e2d441324c0388fa80c3d24f733b4194a4eb7f47dd8ee2b08eb1a24a647c`.

`ContainerModeTest` (`@DatabaseTest @ReferenceToo @Modes({Mode.REFERENCE,
Mode.ORACLE})`). It talks to the database through `java.sql.DriverManager` and a
`jdbc:postgresql://<host>:<port>/<name>` URL built from the `TestDatabase` accessors
(this is the harness testing itself; the pq classes that connect do not exist yet):
- `theClassHasItsOwnDatabase`: `SELECT current_database()` returns `db.name()`, which
  matches `t\d{5}`; `SELECT current_setting('server_version_num'),
  current_setting('server_encoding'), datcollate, datctype FROM pg_database WHERE datname
  = current_database()` returns `180006`, `UTF8`, `C`, `C`.
- `theDatabaseIsEmptyAndWritable`: `SELECT count(*) FROM pg_class WHERE relnamespace =
  'public'::regnamespace` is 0; `CREATE TABLE harness_probe (a int4)` succeeds.
- `referenceModeConnectsThroughTheSupportApi` (`@Modes(Mode.REFERENCE)`): `db.connect()`
  returns a connection whose `getMetaData().getDriverName()` is `PostgreSQL JDBC
  Driver` and `getDriverVersion()` is `42.7.13`; `db.conninfo()` throws
  `IllegalStateException`.
- `oracleModeLoadsLibpq18` (`@Modes(Mode.ORACLE)`): after
  `NativeLoader.ensureLoaded()`: `PqLibrary.prefix()` is `PQ`, `PqLibrary.libVersion()`
  is 180006, `hasFeature` is true for `CLOSE_PREPARED`, `CLOSE_PORTAL`, `SOCKET_POLL`,
  `CURRENT_TIME_USEC`, `CHUNKED_ROWS` and false for `EXT_VERSION` and `EXT_RESTORE`;
  `db.conninfo()` equals `host='127.0.0.1' port='<db.port()>' dbname='<db.name()>'
  user='stuntgres' password='stuntgres' sslmode='disable'`.

Acceptance commands: `MODE=reference` ends with `tests=4 failures=0 errors=0 skipped=1`
(three cases run, the oracle-only case is skipped), `MODE=oracle` with the same numbers
(the reference-only case is skipped); after each run `docker ps -a --filter
label=stuntgres.jdbc.pid --format '{{.Names}}'` prints nothing; `MODE=unit` passes with
the image test; `MODE=reference TEST=ImageTest` exits non-zero with `run_mode: no test of
reference mode matches TEST=ImageTest`; a Docker failure is a failure of the part, not a
skip.

#### JDBC-TEST-01C-SERVER

Files: `tools/mode_server.sh`, `src/test/java/org/stuntgres/testing/ServerBackend.java`,
`src/test/java/org/stuntgres/testing/ServerModeTest.java`. Design sections: 10.5, 10.2.
Uses, without editing, `tools/libpq18.sh` of TEST-01B. Changes nothing under `cmd/`,
`pgserver/` or `internal/`: it only builds and starts the server.

Step. Write the two files as in 10.5.

Tests to write first (`ServerModeTest`, `@DatabaseTest @Modes(Mode.SERVER)`; database
access through `DriverManager` and `jdbc:postgresql://` as in TEST-01B: pgjdbc connects
to the stuntgres server):
- `theClassHasItsOwnDatabase`: `SELECT current_database(), current_user` returns
  `db.name()` (matches `t\d{3}`) and `stuntgres`.
- `serverModeLoadsLibpq18`: as `oracleModeLoadsLibpq18` of TEST-01B.
- `databasesAreSeparateEngines`: create table `only_here (a int4)` in `db`; open a JDBC
  connection to another database of the pool on the same server (`t063`, or `t062` when
  `db.name()` is `t063`; same host, port, user and password): `SELECT count(*) FROM
  pg_class WHERE relname = 'only_here'` is 0.
- `databaseOutsideThePoolIsRefused`: a JDBC connection to database `not_granted` on the
  same server fails with an `SQLException`.

Acceptance commands: `MODE=server` runs the tests of this part and passes (the classes
of TEST-01B are skipped by their `@Modes` gate and show as skipped); afterwards
`pgrep -f 'bindings/jdbc/target/stuntgres serve'` finds nothing and `ls
bindings/jdbc/target | grep -c '^server-'` prints 0; `git status --short` lists nothing
under `bindings/jdbc/target`.

#### JDBC-PQ-04A-CONN-BASE

Files: `src/main/c/sjni_conn.c`, `src/main/java/org/stuntgres/pq/ConnBase.java`,
`src/main/java/org/stuntgres/pq/HandleRelease.java` (stub of 11.2),
`src/main/c/sjni_notice.c` (stub of 11.2),
`src/test/java/org/stuntgres/pq/ConnBaseTest.java`. Design sections: 5.1 rows 4 to 7,
5.2, 7.1, 9.1, 9.5 (`ConnBase`), 9.6 (first paragraph).

Step. Write `sjni_conn.c` as in 5.2, `ConnBase.java` as in 9.5 and the two stubs as in
11.2.

Tests to write first (`ConnBaseTest`, `@DatabaseTest`, package `org.stuntgres.pq`).
`PqConnection` does not exist yet: the test file declares
`private static final class Conn extends ConnBase { Conn(long h) { super(h); } }` and
opens with `new Conn(ConnBase.open(db.conninfo(), 0))`.
- `connectAndClose`: `status()` is `ConnStatus.OK`; `errorMessage()` is empty;
  `isClosed()` false; after `close()` it is true; a second `close()` does nothing.
- `methodsAfterClose`: after `close()`, `status()` and `errorMessage()` each throw
  `PqException` kind 5 with the message `the connection is closed`.
- `unknownDatabase`: `ConnBase.open(db.conninfo().replace("dbname='" + db.name() + "'",
  "dbname='no_such_db'"), 0)` throws kind 9;
  the message starts with `connection to server at "127.0.0.1", port ` and ends with
  ` failed: FATAL:  database "no_such_db" does not exist`.
- `malformedConninfo`: `ConnBase.open("this is not a conninfo", 0)` throws kind 9 with
  the message `missing "=" after "this" in connection info string`.
- `nulInConninfo`: `ConnBase.open("host=a\0b", 0)` throws kind 6 `the connection string
  contains a NUL character`.
- `timeLimit`: `ConnBase.open("host=10.255.255.1 port=5432 dbname=x user=x", 300)`
  throws kind 9 with the message `timeout expired`, not earlier than 300 ms and within
  5 s (measure with `System.nanoTime`). When the host has a route that refuses the
  address at once, the message is libpq's refusal text instead: accept kind 9 with any
  message in less than 300 ms as well.
- `manyConnections`: 20 connections opened and closed in a loop all have status OK.
- `nothingIsPrintedForAStartupNotice` is not tested here (it needs `takeNotices`, PQ-08).

Run in oracle and server mode.

#### JDBC-PQ-04B-SESSION

Files: `src/main/c/sjni_session.c`, `src/main/java/org/stuntgres/pq/ConnSession.java`,
`PqConnection.java`, the seven stubs of 11.2 (`ConnNotices.java`, `ConnExec.java`,
`ConnParams.java`, `ConnPrepared.java`, `ConnNotify.java`, `ConnCopy.java`,
`ConnEscape.java`), `src/test/java/org/stuntgres/pq/PqConnectionTest.java`. Design
sections: 5.1 rows 8 to 11, 5.3, 9.5.

Step. Write `sjni_session.c` as in 5.3, `ConnSession.java` and `PqConnection.java` as
in 9.5 and the seven stubs as in 11.2.

Tests to write first (`PqConnectionTest`, `@DatabaseTest`):
- `sessionFacts`: `transactionStatus()` is `TransactionStatus.IDLE`; `serverVersion()`
  is at least 180000; `backendPid()` is positive; `parameterStatus("server_encoding")`
  is `UTF8`; `parameterStatus("client_encoding")` is `UTF8`;
  `parameterStatus("standard_conforming_strings")` is `on`;
  `parameterStatus("no_such_parameter")` is null; `parameterStatus("server_version")`
  is not null.
- `parameterNameWithNul`: `parameterStatus("a\0b")` throws kind 6 `the parameter name
  contains a NUL character`.
- `twoConnectionsAreTwoSessions`: two connections to `db` have different `backendPid()`.
- `connectWithTimeLimit`: `PqConnection.connect(db.conninfo(), 10000)` has status OK.
- `closedConnection`: after `close()`, `transactionStatus()`, `parameterStatus("x")`,
  `serverVersion()` and `backendPid()` each throw kind 5 `the connection is closed`.
- `connectFailure`: as `unknownDatabase` of PQ-04A through `PqConnection.connect`.

Run in oracle and server mode.

#### JDBC-PQ-05A-RESULT-CLASS

Files: `src/main/java/org/stuntgres/pq/PqResult.java`,
`src/test/java/org/stuntgres/pq/PqResultTest.java`. Design sections: 6.1.

Step. Write `PqResult.java` as in 6.1.

Tests to write first (`PqResultTest`, `@UnitTest`, same package: it calls the
package-private constructor). Fixture `R`: status 2, command status `SELECT 2`, command
tuples `2`, OID 0, 2 columns, 2 rows; field names `xy` (bytes); field meta
`{1, 16384, 1, 23, 4, -1, 0,  2, 16384, 2, 25, -1, -1, 0}`; data `1one2` (bytes); ends
`{1, 4, 5, ~5}`; no parameter types, empty error fields and message.
- `header`: `status()` 2; `commandStatus()` `SELECT 2`; `commandTuples()` `2`;
  `oidValue()` 0; `columnCount()` 2; `rowCount()` 2.
- `fields`: `fieldName(0)` `x`, `fieldName(1)` `y`; `fieldIndex("y")` 1,
  `fieldIndex("Y")` -1; `tableOid(0)` 16384; `tableColumn(1)` 2; `typeOid(0)` 23,
  `typeOid(1)` 25; `typeSize(0)` 4, `typeSize(1)` -1; `typeModifier(0)` -1; `format(1)`
  0.
- `values`: `string(0, 0)` `1`; `string(0, 1)` `one`; `string(1, 0)` `2`; `isNull(1, 1)`
  true and `string(1, 1)`, `bytes(1, 1)` null; `length(0, 1)` 3; `offset(0, 1)` 1;
  `offset(1, 0)` 4; `length(1, 1)` 0; `bytes(0, 1)` equals `utf8("one")` and is a copy
  (changing it does not change `string(0, 1)`); `data()` has length 5.
- `nullAgainstEmpty`: one row, two columns, data empty, ends `{0, ~0}`: `isNull(0, 0)`
  false with `string(0, 0)` empty and `bytes(0, 0)` of length 0; `isNull(0, 1)` true.
- `nullFirst`: ends `{~0, 3}`, data `abc`: `isNull(0, 0)` true; `string(0, 1)` `abc`;
  `offset(0, 1)` 0.
- `outOfRange`: `string(2, 0)`, `string(0, 2)`, `fieldName(2)`, `isNull(-1, 0)` each
  throw `IndexOutOfBoundsException`.
- `unsignedOid`: a result with OID `-1` (bits) has `oidValue()` 4294967295.
- `describeResult`: parameter types `{23, 25}`: `parameterCount()` 2,
  `parameterType(1)` 25.
- `errorResult`: status 7, error fields packed `S` `ERROR` 0 `V` `ERROR` 0 `C` `22012` 0
  `M` `division by zero` 0, message `ERROR:  division by zero\n`: `sqlState()` `22012`;
  `errorField(DiagField.MESSAGE_PRIMARY)` `division by zero`;
  `errorField(DiagField.MESSAGE_HINT)` null; `errorMessage()` `ERROR:  division by
  zero`; for `R`: `sqlState()` null and `errorMessage()` empty.
- `nonBmpAndMalformedText`: data `f0 9f 98 80` gives a string of length 2 equal to
  `"😀"`; data `63 61 66 e9` gives `caf�` while `bytes` returns the four
  bytes.

Run in unit mode.

#### JDBC-PQ-05B-RESULT-COPY

Files: `src/main/c/sjni_result.c`, `src/main/java/org/stuntgres/pq/ResultCopy.java`,
`src/test/java/org/stuntgres/pq/ResultCopyTest.java`. Design sections: 5.1 row 12, 6.2,
6.3, 6.1 ("Things to know").

Step. Write both files as in 6.2.

Tests to write first (`ResultCopyTest`, `@DatabaseTest`; `x(sql)` is
`ResultCopy.exec0(conn.handle(), utf8(sql))` on a `PqConnection`):
- `rowsAndFields`: `x("SELECT 1 AS a")`: status TUPLES_OK, command status `SELECT 1`,
  command tuples `1`, 1 column, 1 row, `fieldName(0)` `a`, `typeOid(0)` 23,
  `typeSize(0)` 4, `typeModifier(0)` -1, `format(0)` 0, `tableOid(0)` 0,
  `tableColumn(0)` 0, `string(0, 0)` `1`.
- `tableColumns`: after `x("CREATE TABLE t (x int4, y text)")` (status COMMAND_OK,
  command status `CREATE TABLE`, command tuples empty) and `x("INSERT INTO t VALUES (1,
  'one'), (2, NULL)")` (COMMAND_OK, `INSERT 0 2`, tuples `2`): `x("SELECT x, y FROM t
  ORDER BY x")` has command status `SELECT 2`, columns `x` (type 23, size 4,
  `tableColumn` 1, `tableOid` not 0) and `y` (type 25, size -1, `tableColumn` 2), rows
  `1`, `one` and `2`, NULL.
- `lastResultOfAString`: `x("SELECT 1; SELECT 2 AS two")` has `fieldName(0)` `two` and
  value `2`.
- `emptyStatement`: `x("")` has status EMPTY_QUERY, 0 columns, 0 rows, empty command
  status.
- `errorFields`: `x("SELECT 1/0")`: status FATAL_ERROR; fields `S` `ERROR`, `V` `ERROR`,
  `C` `22012`, `M` `division by zero`; `errorMessage()` `ERROR:  division by zero`; no
  columns, no rows. `x("SELECT * FROM nope")`: `C` `42P01`, `M` `relation "nope" does not
  exist`, `P` `15`; `errorMessage()` is the three lines `ERROR:  relation "nope" does not
  exist`, `LINE 1: SELECT * FROM nope` and a line of 22 spaces followed by `^`.
  `x("SELEC 1")`: `C` `42601`, `M` `syntax error at or near "SELEC"`, `P` `1`.
- `nullAgainstEmpty`: `x("SELECT ''::text AS e, NULL::text AS n, ' '::text AS sp")`:
  `string(0, 0)` empty and not null, `isNull(0, 1)`, `string(0, 2)` one space.
- `text`: `x("SELECT '😀' AS \"col😀\"")`: `fieldName(0)` is
  `col` followed by the emoji, `fieldIndex` finds it, the value is the emoji and
  `bytes(0, 0)` is `f0 9f 98 80`.
- `largeValue`: `x("SELECT repeat('x', 10485760) AS big, 'tail' AS t")`: `length(0, 0)`
  10485760, every byte `x`, `string(0, 1)` `tail`, `offset(0, 1)` 10485760.
- `manyRows`: `x("SELECT i, CASE WHEN i % 3 = 0 THEN NULL ELSE repeat('y', i % 50) END
  FROM generate_series(1, 10000) AS g(i)")`: 10000 rows; for every row `string(r, 0)` is
  `r + 1` in decimal, and column 1 is NULL exactly when `(r + 1) % 3 == 0`, else
  `(r + 1) % 50` letters `y`.
- `describeLikeResultHasNoRows` is covered by PQ-07.
- `nulInStatement` is covered by PQ-06A (the check is in `ConnExec`).

Run in oracle and server mode.

#### JDBC-PQ-06A-EXEC

Files: `src/main/c/sjni_exec.c`, `src/main/java/org/stuntgres/pq/ConnExec.java`
(replaces the stub), `src/test/java/org/stuntgres/pq/ConnExecTest.java`. Design
sections: 5.1 rows 13 and 14, 5.4, 9.5 (`exec`, `sendQuery`, `getResult`, `execAll`).

Step. Write `sjni_exec.c` as in 5.4 and `ConnExec.java` as in 9.5.

Tests to write first (`ConnExecTest`, `@DatabaseTest`):
- `execReturnsTheLastResult`: `conn.exec("SELECT 1; SELECT 2 AS two")`: TUPLES_OK,
  `fieldName(0)` `two`, value `2`.
- `everyResultOfAString`: `conn.execAll("SELECT 1 AS a; CREATE TEMP TABLE t (x int4, y
  text); INSERT INTO t VALUES (1, 'one'), (2, NULL); SELECT x, y FROM t ORDER BY x")`
  returns four results: TUPLES_OK `SELECT 1` with value `1`; COMMAND_OK `CREATE TABLE`;
  COMMAND_OK `INSERT 0 2` with command tuples `2`; TUPLES_OK `SELECT 2` with rows `1`,
  `one` and `2`, NULL. `transactionStatus()` afterwards is IDLE.
- `errorInTheMiddleEndsTheString`: `conn.execAll("SELECT 1; SELECT 1/0; SELECT 3")`
  returns two results: TUPLES_OK with `1`, then FATAL_ERROR with SQLSTATE `22012`.
- `emptyString`: `conn.execAll("")` returns one result with status EMPTY_QUERY.
- `sendAndCollectByHand`: `sendQuery("SELECT 1; SELECT 2")`; `getResult()` twice gives
  values `1` and `2`; a third `getResult()` is null; a fourth is null too.
- `transactionStatusFollowsTheSession`: after `exec("BEGIN")`: INTRANS; after
  `exec("SELECT 1/0")`: INERROR; after `exec("ROLLBACK")`: IDLE.
- `nulInStatement`: `exec("SELECT '\0'")` and `sendQuery("SELECT '\0'")` throw kind 6
  `the statement contains a NUL character`; the connection still answers `SELECT 1`.
- `loneSurrogateBecomesQuestionMark`: `exec("SELECT 'lone \uD800 surrogate'")` returns
  `lone ? surrogate`.
- `lostConnection`: a second connection `victim`; `conn.exec("SELECT
  pg_terminate_backend(" + victim.backendPid() + ")")` returns `t`; then
  `victim.exec("SELECT 1")` either returns a FATAL_ERROR result or throws kind 8;
  afterwards `victim.status()` is BAD; a further `victim.exec("SELECT 1")` throws kind 8
  with the message `the command failed: no connection to the server`, and
  `victim.sendQuery("SELECT 1")` throws kind 8 with `sending the statement failed: no
  connection to the server`.
- `closedConnection`: after `close()`, `exec`, `sendQuery`, `getResult` and `execAll`
  throw kind 5 `the connection is closed`.

Run in oracle and server mode.

#### JDBC-PQ-06B-PARAMS

Files: `src/main/c/sjni_params.c`, `src/main/java/org/stuntgres/pq/ConnParams.java`
(replaces the stub), `src/test/java/org/stuntgres/pq/ConnParamsTest.java`. Design
sections: 5.1 row 15, 5.5, 9.5 (`execParams`, `checkParams`).

Step. Write `sjni_params.c` as in 5.5 and `ConnParams.java` as in 9.5.

Tests to write first (`ConnParamsTest`, `@DatabaseTest`):
- `typedAndUntyped`: `execParams("SELECT $1::int4 + 1 AS n, $2::text AS t, $3::text IS
  NULL AS isnull, $4::text AS empty", new int[] {23, 0, 0, 25}, {utf8("41"),
  utf8("zażółć 😀"), null, new byte[0]}, null)`:
  TUPLES_OK, column types 23, 25, 16, 25, values `42`, the same text, `t`, and an empty
  non-null string.
- `serverDecidesTypes`: `execParams("SELECT $1 AS untyped", null, {utf8("x")}, null)`:
  column type 25, value `x`.
- `noParameters`: `execParams("SELECT 7", null, new byte[0][], null)` returns `7`.
- `nullAgainstEmpty`: `execParams("SELECT $1::text IS NULL, $2::text IS NULL, $2::text =
  ''", null, {null, new byte[0]}, null)`: `t`, `f`, `t`.
- `conversionErrorIsAResult`: `exec("CREATE TEMP TABLE t (x int4, y text)")`, then
  `execParams("INSERT INTO t VALUES ($1, $2)", null, {utf8("abc"), null}, null)`:
  FATAL_ERROR, SQLSTATE `22P02`, primary message `invalid input syntax for type integer:
  "abc"`, context `unnamed portal parameter $1 = '...'`.
- `binaryFormat`: `execParams("SELECT $1 + 1", new int[] {23}, {{0, 0, 1, 0}}, new int[]
  {1})` returns `257`.
- `nulInATextParameterIsRefused`: `execParams("SELECT $1", null, {{'a', 0, 'b'}}, null)`
  throws kind 6 `parameter $1 contains a NUL character`.
- `nulInABinaryParameterReachesTheServer`: the same value with formats `{1}` for
  `SELECT $1::text`: FATAL_ERROR, SQLSTATE `22021`, primary message `invalid byte
  sequence for encoding "UTF8": 0x00`, context `unnamed portal parameter $1`.
- `argumentChecks`: types `{1, 2}` for one value: kind 6 `got 2 parameter types for 1
  values`; formats `{0, 0}` for one value: `got 2 parameter formats for 1 values`;
  format 2: `parameter $1 has format 2 (0 or 1 expected)`; 65536 values (each
  `utf8("1")`): `too many parameters: 65536 (the limit is 65535)`.
- `largeParameter`: a 10485760-byte value of the letters `a` to `z` repeating:
  `execParams("SELECT length($1::text), md5($1::text), $1::text", ...)`: `10485760`,
  `d3344b5243db57263ef171b989c41be4`, and the value byte for byte.
- `manyParameters`: 1000 values, value `i` the decimal text of `i` (0 to 999), for the
  statement `SELECT $1::int4 + $1000::int4, $2::int4, $3::int4, ..., $999::int4` (every
  parameter is used): TUPLES_OK with 999 columns, the first value `999`, the last `998`.
- `parameterCountMismatchIsAResult`: `execParams("SELECT 1", null, new byte[65535][],
  null)` (65535 SQL NULLs for a statement without placeholders): FATAL_ERROR, SQLSTATE
  `08P01`, primary message `bind message supplies 65535 parameters, but prepared
  statement "" requires 0`; the connection stays usable.

Run in oracle and server mode.

#### JDBC-PQ-07-PREPARED

Files: `src/main/c/sjni_prepared.c`, `src/main/java/org/stuntgres/pq/ConnPrepared.java`
(replaces the stub), `src/test/java/org/stuntgres/pq/ConnPreparedTest.java`. Design
sections: 5.1 rows 16 to 19, 5.6, 9.5.

Step. Write `sjni_prepared.c` as in 5.6 and `ConnPrepared.java` as in 9.5.

Tests to write first (`ConnPreparedTest`, `@DatabaseTest`; setup per test: `CREATE TEMP
TABLE t (x int4, y text)` and `INSERT INTO t VALUES (1, 'one'), (2, NULL)`):
- `prepareDescribeExecuteClose`: `prepare("s1", "SELECT $1::int4 AS a, $2 AS b, x FROM t
  WHERE x > $1", new int[] {0, 25})`: COMMAND_OK with an empty command status.
  `describePrepared("s1")`: COMMAND_OK, 0 rows, columns `a` (23), `b` (25), `x` (23,
  `tableColumn` 1), `parameterCount()` 2 with types 23 and 25.
  `execPrepared("s1", {utf8("1"), utf8("b")}, null)`: TUPLES_OK `SELECT 1`, one row `1`,
  `b`, `2`. `closePrepared("s1")`: COMMAND_OK. `execPrepared("s1", ...)` again:
  FATAL_ERROR, SQLSTATE `26000`, message `prepared statement "s1" does not exist`.
- `closeUnknownIsNotAnError`: `closePrepared("never_existed")` is COMMAND_OK;
  `describePrepared("never_existed")` is FATAL_ERROR `26000` with `prepared statement
  "never_existed" does not exist`.
- `unnamedStatement`: `prepare("", "SELECT $1::int4 * 2", null)` then `execPrepared("",
  {utf8("21")}, null)` returns `42`.
- `prepareErrorIsAResult`: `prepare("bad", "SELEC 1", null)`: FATAL_ERROR `42601`.
- `duplicateName`: preparing `s2` twice: the second is FATAL_ERROR `42P05`, message
  `prepared statement "s2" already exists`.
- `binaryParameter`: `prepare("s3", "SELECT $1::int4 + 1", new int[] {23})`;
  `execPrepared("s3", {{0, 0, 1, 0}}, new int[] {1})` returns `257`.
- `argumentChecks`: a statement name with a NUL: kind 6 `the statement name contains a
  NUL character`; a text value with a NUL: `parameter $1 contains a NUL character`.
- The two tests that call `closePrepared` carry
  `@RequiresFeature(Feature.CLOSE_PREPARED)`; with libpq 18 and libspq nothing is skipped.

Run in oracle and server mode.

#### JDBC-PQ-08-NOTICES

Files: `src/main/c/sjni_notice.c` (replaces the stub),
`src/main/java/org/stuntgres/pq/ConnNotices.java` (replaces the stub),
`src/test/java/org/stuntgres/pq/ConnNoticesTest.java`,
`src/test/java/org/stuntgres/pq/NoticeProbe.java`. Design sections: 5.1 row 20, 7.2,
9.5 (`takeNotices`).

Step. Write `sjni_notice.c` as in 7.2 and `ConnNotices.java` as in 9.5.

Tests to write first (`ConnNoticesTest`, `@DatabaseTest`):
- `noticeAndWarning`: `exec("DO $$ BEGIN RAISE NOTICE 'hello %', 42; RAISE WARNING
  'careful' USING HINT = 'a hint', DETAIL = 'a detail'; END $$")` is COMMAND_OK with
  command status `DO`; `takeNotices()` has two elements. First: fields `S` `NOTICE`, `V`
  `NOTICE`, `C` `00000`, `M` `hello 42`, `W` `PL/pgSQL function inline_code_block line 1
  at RAISE`; `message()` `NOTICE:  hello 42`. Second: `S` `WARNING`, `C` `01000`, `M`
  `careful`, `D` `a detail`, `H` `a hint`; `message()` is the three lines
  `WARNING:  careful`, `DETAIL:  a detail`, `HINT:  a hint`.
- `takeEmptiesTheQueue`: a second `takeNotices()` returns an empty list; with no notice
  at all the list is empty.
- `noticesOfSeveralCallsAccumulateInOrder`: three `DO` blocks raising `one`, `two`,
  `three`, then one `takeNotices()`: three notices with those primary messages in that
  order.
- `noticesBelongToTheirConnection`: a notice raised on one connection is not returned by
  another connection's `takeNotices()`.
- `manyNotices`: `DO $$ BEGIN FOR i IN 1..1000 LOOP RAISE NOTICE 'n%', i; END LOOP; END
  $$`: 1000 notices, the first `n1`, the last `n1000`.
- `closedConnection`: `takeNotices()` after `close()` throws kind 5.
- `aWarningAtSessionStartIsQueuedNotPrinted` (`@Modes(Mode.ORACLE)`): on the test's
  connection run `ALTER ROLE stuntgres IN DATABASE "<db.name()>" SET
  default_text_search_config = 'no_such_config'`; then run `NoticeProbe` in a child JVM
  (`ChildJvm.run` with the three `stuntgres.test.native.*` properties of the parent and
  the argument `db.conninfo()`). `NoticeProbe` connects, prints `notices=<n>` and each
  notice's `message()` on its own line. Expected output, exactly, two lines: `notices=1`
  and
  `WARNING:  invalid value for parameter "default_text_search_config": "no_such_config"`.
  The child's standard error is part of the output, so a warning printed by the library
  would appear as an extra line. The setting is scoped to the test's own database.

Run in oracle and server mode.

#### JDBC-PQ-09-CANCEL

Files: `src/main/c/sjni_cancel.c`, `src/main/java/org/stuntgres/pq/PqCancel.java`,
`src/test/java/org/stuntgres/pq/PqCancelTest.java`. Design sections: 5.1 rows 21 to 23,
7.3, 9.6 (`PqCancel`).

Step. Write both files as in 7.3 and 9.6.

Tests to write first (`PqCancelTest`, `@DatabaseTest`):
- `cancelFromASecondThread`: `PqCancel cancel = PqCancel.of(conn)`; a thread runs
  `conn.exec("SELECT pg_sleep(30)")` and stores the result in an `AtomicReference`; the
  test thread calls `Waits.until(20000, "the cancelled statement", () -> {
  cancel.cancel(); return ref.get() != null; })`; then joins the thread. The result is
  FATAL_ERROR, SQLSTATE `57014`, primary message `canceling statement due to user
  request`; the whole test takes less than 10 s; afterwards `conn.exec("SELECT 'still
  here'")` returns `still here` and `transactionStatus()` is IDLE.
- `cancelWhenIdleDoesNothing`: `cancel.cancel()` returns; `conn.exec("SELECT 2")` returns
  `2`.
- `closeIsIdempotentAndCancelAfterCloseFails`: `close()` twice (on a plain variable);
  `cancel()` then throws kind 5 `the cancel handle is closed`.
- `handleOutlivesItsConnection`: `conn.close()`, then `cancel.cancel()` returns normally
  (measured in oracle and server mode: the request reaches the server, which ignores
  it), and `cancel.close()` works.
- `ofAClosedConnection`: `PqCancel.of(conn)` after `conn.close()` throws kind 5 `the
  connection is closed`.
- `twoHandlesOfOneConnection`: both can cancel; closing one leaves the other usable.

Run in oracle and server mode.

#### JDBC-PQ-10-NOTIFY

Files: `src/main/c/sjni_notify.c`, `src/main/java/org/stuntgres/pq/ConnNotify.java`
(replaces the stub), `src/test/java/org/stuntgres/pq/ConnNotifyTest.java`. Design
sections: 5.1 row 24, 7.4, 9.5 (`notifies`).

Step. Write `sjni_notify.c` as in 7.4 and `ConnNotify.java` as in 9.5.

Tests to write first (`ConnNotifyTest`, `@DatabaseTest`):
- `ownNotifications`: `exec("LISTEN ch")`; `exec("NOTIFY ch, 'p1'; NOTIFY ch")`;
  `notifies(0)` returns two notifications: channel `ch`, payloads `p1` and empty, both
  with `backendPid()` equal to `conn.backendPid()`. A second `notifies(0)` is empty.
- `waitTimesOut`: with nothing pending, `notifies(200)` returns an empty list after at
  least 200 ms (and less than 5 s).
- `notificationFromAnotherSession`: `other.exec("NOTIFY ch, 'from other'")` on a second
  connection; `conn.notifies(10000)` returns one notification, payload `from other`,
  `backendPid()` equal to `other.backendPid()`.
- `waitEndsWhenANotificationArrives`: a thread calls `conn.notifies(20000)` and stores
  the list; the test thread then runs `other.exec("NOTIFY ch, 'late'")` and joins the
  thread with a bound of 15 s. The list has one element with payload `late`. (Whichever
  comes first, the wait or the `NOTIFY`, the result is the same: no synchronization is
  needed.)
- `notListening`: `other.exec("NOTIFY nobody, 'x'")` for a channel `conn` does not
  listen to: `conn.notifies(100)` is empty.
- `closedConnection`: `notifies(0)` after `close()` throws kind 5.

Run in oracle and server mode.

#### JDBC-PQ-11-COPY

Files: `src/main/c/sjni_copy.c`, `src/main/java/org/stuntgres/pq/ConnCopy.java`
(replaces the stub), `src/test/java/org/stuntgres/pq/ConnCopyTest.java`. Design
sections: 5.1 rows 25 to 27, 7.5, 9.5.

Step. Write `sjni_copy.c` as in 7.5 and `ConnCopy.java` as in 9.5.

Tests to write first (`ConnCopyTest`, `@DatabaseTest`; setup: `CREATE TEMP TABLE t (x
int4, y text)`):
- `copyIn`: `execAll("COPY t FROM STDIN")` returns one result, status COPY_IN, 2 columns
  with empty names; `putCopyData(utf8("10\tten\n11\t\\N\n"), 0, length)`;
  `putCopyEnd(null)`; `getResult()` is COMMAND_OK with command status `COPY 2` and
  command tuples `2`; the next `getResult()` is null; `SELECT count(*) FROM t` is `2`.
- `copyInInPieces`: the same 13 bytes sent as `putCopyData(rows, 0, 4)`,
  `putCopyData(rows, 4, 0)`, `putCopyData(rows, 4, 5)`, `putCopyData(rows, 9, 4)`: again
  `COPY 2`, and `SELECT count(*), min(y) FROM t` returns `2` and `ten`.
- `copyOut`: after inserting `(1, 'one'), (2, NULL)`: `execAll("COPY (SELECT x, y FROM t
  ORDER BY x) TO STDOUT")` returns one COPY_OUT result; `getCopyData()` returns
  `utf8("1\tone\n")`, then `utf8("2\t\\N\n")`, then null; `getResult()` is COMMAND_OK
  `COPY 2`; the next is null.
- `copyFail`: `execAll("COPY t FROM STDIN")`, `putCopyEnd("stop it")`: `getResult()` is
  FATAL_ERROR, SQLSTATE `57014`, primary message `COPY from stdin failed: stop it`,
  context `COPY t, line 1`; the next `getResult()` is null and `transactionStatus()` is
  IDLE.
- `statementsAfterTheCopyInOneString`: `execAll("COPY t FROM STDIN; SELECT 5")` stops at
  the COPY_IN result; after `putCopyEnd(null)`, `getResult()` gives COMMAND_OK `COPY 0`,
  then TUPLES_OK with `5`, then null.
- `argumentChecks`: `putCopyData(new byte[4], 2, 3)` throws
  `IndexOutOfBoundsException`; `putCopyEnd("a\0b")` throws kind 6 `the error message
  contains a NUL character` (run inside a COPY, then end it with `putCopyEnd("x")`).
- `closedConnection`: the three methods after `close()` throw kind 5.

Run in oracle and server mode.

#### JDBC-PQ-12-ESCAPE

Files: `src/main/c/sjni_escape.c`, `src/main/java/org/stuntgres/pq/ConnEscape.java`
(replaces the stub), `src/test/java/org/stuntgres/pq/ConnEscapeTest.java`. Design
sections: 5.1 rows 28 to 30, 5.7, 9.5.

Step. Write `sjni_escape.c` as in 5.7 and `ConnEscape.java` as in 9.5.

Tests to write first (`ConnEscapeTest`, `@DatabaseTest`; Java source notation):
- `literal`: `escapeLiteral("it's a \\ test")` equals `" E'it''s a \\\\ test'"`;
  `escapeLiteral("")` equals `"''"`; `escapeLiteral("plain")` equals `"'plain'"`;
  `escapeLiteral("😀")` equals `"'😀'"`.
- `identifier`: `escapeIdentifier("a\"b")` equals `"\"a\"\"b\""`;
  `escapeIdentifier("Mixed Case")` equals `"\"Mixed Case\""`.
- `nul`: `escapeLiteral("a\0b")` throws kind 6 `the text contains a NUL character`;
  `escapeIdentifier("a\0b")` throws kind 6 `the identifier contains a NUL character`.
- `clientEncoding`: `clientEncoding()` is `UTF8`; after `setClientEncoding("LATIN1")` it
  is `LATIN1`; after `setClientEncoding("UTF8")` it is `UTF8` again.
- `unknownEncoding`: `setClientEncoding("NOPE")` throws kind 8; the message starts with
  `setting the client encoding failed: ` and contains `NOPE`; `clientEncoding()` is
  still `UTF8` and the connection has status OK.
- `closedConnection`: the four methods after `close()` throw kind 5.

Run in oracle and server mode. (`ConnExec` may still be a stub when this part runs: the
tests use no statement.)

#### JDBC-PQ-13-CLEANER

Files: `src/main/java/org/stuntgres/pq/HandleRelease.java` (replaces the stub),
`src/test/java/org/stuntgres/pq/HandleReleaseTest.java`,
`src/test/java/org/stuntgres/pq/ExitProbe.java`. Design sections: 9.1 (Handles,
Reachability), 9.6.

Step. Replace the stub with the file of 9.6.

Tests to write first (`HandleReleaseTest`, `@DatabaseTest`; `alive(pids)` is `SELECT
count(*) FROM pg_stat_activity WHERE pid IN (<pids>)` on a second connection `watcher`):
- `aForgottenConnectionIsFinished`: a helper method opens a connection, returns its
  `backendPid()` and drops the reference; `alive(pid)` is `1` right after; then
  `Waits.until(20000, "the cleaner", () -> { System.gc(); return
  alive(pid).equals("0"); })`.
- `aCallInProgressKeepsItsConnection`: a thread runs
  `PqConnection.connect(db.conninfo()).exec("SELECT pg_sleep(1.5), 'finished'")` without
  keeping the connection in a variable; the test thread calls `Waits.until(20000, "the
  statement", () -> { System.gc(); return done.get(); })`; the result is TUPLES_OK.
- `closeThenCollect`: close a connection explicitly and drop the reference; then run the
  wait of `aForgottenConnectionIsFinished` for another, forgotten connection. It passes:
  the cleaner runs for both objects and the closed handle is not freed again.
- `releaseActionRunsOnce` (no database needed, but in this class): a counter-incrementing
  `Runnable` registered with `HandleRelease.register(new Object(), action)`: running the
  returned runnable three times leaves the counter at 1.
- `jvmExitWithOpenConnections`: run `ExitProbe` in a child JVM (seam properties passed
  on) with the arguments `db.conninfo()` and `return`, then again with `exit`.
  `ExitProbe` opens three connections, prints `pids <a> <b> <c>`, starts a transaction
  that creates a table on the first, blocks a daemon thread in `SELECT pg_sleep(300)` on
  the third, waits (spinning on a query of `pg_stat_activity`) until that statement is
  active, registers a shutdown hook that prints `hook ran`, prints `ending by <how>` and
  returns from `main` or calls `System.exit(0)`. Expected: exit code 0; the output lines
  after the first are `ending by <how>` and `hook ran`; `Waits.until(10000, ...)` sees
  `alive(a, b)` reach `0`; `SELECT count(*) FROM pg_class WHERE relname = 'left_open'`
  is `0`. Do not assert anything about session `c` (PostgreSQL keeps it until the sleep
  ends); end it with `SELECT pg_terminate_backend(c)`.

Run in oracle and server mode.

#### JDBC-PQ-14A-CLUSTER

Files: `src/main/c/sjni_cluster.c`, `src/main/java/org/stuntgres/pq/PqCluster.java`,
`src/test/c/fake_cluster.c`, `src/test/java/org/stuntgres/pq/PqClusterTest.java`,
`src/test/java/org/stuntgres/pq/ClusterProbe.java`. Design sections: 5.1 rows 31 and
32, 5.8, 9.7, 10.9.

Step. Write `sjni_cluster.c` as in 5.8, `PqCluster.java` as in 9.7 and
`fake_cluster.c` as in 10.9. `tools/build_test_natives.sh` picks the new C file up by
itself.

`ClusterProbe` (main class): prints `version=<PqCluster.version()>` or `version failed
kind=<kind> message=<message>`; then for each argument calls `PqCluster.drop(argument,
"5432")` and prints `dropped <argument>` or `drop failed kind=<kind> message=<message>`.

Tests to write first (`PqClusterTest`, `@UnitTest`; child JVMs with the test seam: shim
`N/full/libstuntgres_jni.so`, library as named):
- `versionAndDropAgainstTheStandIn`: library `N/full/libspq.so`, arguments `c1 busy`:
  output `version=0.0.0-fake\ndropped c1\ndrop failed kind=8 message=dropping the
  cluster failed: cluster "busy" (port 5432) has open connections\n`.
- `aLibraryWithoutTheExtensions`: library `N/noopt/libspq.so`, argument `c1`: output
  `version failed kind=10 message=the loaded library does not provide SPQXversion\ndrop
  failed kind=10 message=the loaded library does not provide SPQXclusterDrop\n`.

Run in unit mode. Against libspq the two methods are exercised by TEST-02.

#### JDBC-TEST-02-NATIVE

Files: `tools/mode_native.sh`, `src/test/java/org/stuntgres/testing/NativeBackend.java`,
`src/test/java/org/stuntgres/testing/NativeModeTest.java`. Design sections: 10.8, 10.2.
Needs `build/libspq/lib/libspq.so` (`make libspq` in the repository root).

Step. Write the two files as in 10.8. They were not run by the design: if one of them
does not work as written against the real libspq, fix it within these files and say so
in the commit body; a fault outside them is a stop condition.

Tests to write first (`NativeModeTest`, `@DatabaseTest @NativeOnly`):
- `nativeModeLoadsLibspq`: after `NativeLoader.ensureLoaded()`: `PqLibrary.prefix()` is
  `SPQ`; all ten features are present; `PqCluster.version()` is `0.1.0`.
- `theClassHasItsOwnCluster`: `db.host()` matches `c\d{5}`, `db.port()` is 5432,
  `db.name()` is `test`; `PqConnection.connect(db.conninfo())` has status OK and
  `parameterStatus("server_encoding")` is `UTF8`.
- `clustersShareNothing`: create a table through a connection to `db`; a connection to
  another cluster (the conninfo of `db` with `host` replaced by `db.host() + "x"`) does
  not see it (`SELECT count(*) FROM pg_class WHERE relname = ...` is `0`); close that
  connection and `PqCluster.drop(db.host() + "x", "5432")` succeeds.
- `dropFailsWhileConnected`: with a connection to the other cluster open,
  `PqCluster.drop` throws kind 8 whose message starts with `dropping the cluster failed:
  `; after closing the connection it succeeds; a second drop succeeds too (CABI 3.2: a
  cluster that never existed counts as dropped).

Acceptance commands: `make libspq` in the repository root, then `make -C bindings/jdbc
check MODE=native` passes and prints the summary line; `MODE=unit`, `reference`,
`oracle`, `server` still pass (the class is not selected there). Without the library the
run ends with `run_mode: cannot start native mode: <root>/build/libspq/lib/libspq.so does
not exist; build it with 'make libspq' in the repository root`.

After this part every `@DatabaseTest` class of the suite that is not gated runs in
native mode too. A test that passes in oracle mode and fails in native mode without a
fault of the binding is a libspq or engine gap: report it (R8); do not change the test.

#### JDBC-PQ-14B-EXT

Files: `src/main/c/sjni_ext.c`, `src/main/java/org/stuntgres/pq/PqExt.java`,
`src/test/java/org/stuntgres/pq/PqExtTest.java`. Design sections: 5.1 rows 33 to 35,
5.8, 9.7.

Step. Write both files as in 5.8 and 9.7 (they compiled in the design's scratch tree and
were not run).

Tests to write first (`PqExtTest`, `@DatabaseTest`):
- `withoutTheExtensions` (`@Modes({Mode.ORACLE, Mode.SERVER})`): `PqExt.reset(conn)`,
  `PqExt.dump(conn)` and `PqExt.restore(conn, new byte[0])` each throw kind 10 with the
  messages `the loaded library does not provide PQXreset`, `... PQXdump`, `...
  PQXrestore`.
- `resetRemovesData` (`@Modes(Mode.NATIVE)`): create a table and a row; `PqExt.reset`;
  `SELECT count(*) FROM pg_class WHERE relname = '<table>'` is `0`; the connection is
  still usable.
- `dumpAndRestore` (`@Modes(Mode.NATIVE)`): create table `d (a int4)` with rows 1 and 2;
  `byte[] dump = PqExt.dump(conn)` is not empty; `DROP TABLE d`; `PqExt.restore(conn,
  dump)`; `SELECT sum(a) FROM d` is `3`.
- `restoreOfGarbageFails` (`@Modes(Mode.NATIVE)`): `PqExt.restore(conn, utf8("not a
  dump"))` throws kind 8 whose message starts with `restoring the database failed: `;
  the connection stays usable.
- `closedConnection` (`@Modes(Mode.NATIVE)`): the three methods on a closed connection
  throw kind 5.

Acceptance commands: `MODE=native TEST=PqExtTest` passes; `MODE=oracle TEST=PqExtTest`
and `MODE=server TEST=PqExtTest` pass with the native-only cases skipped.


## 12. Experiments behind this design

All experiments ran on 2026-11-11 on the tree at commit `0c704a82ce`. Commands and
output are in `.cswd/tmp/JDBC-DESIGN-NATIVE/log.txt`; the sources are next to it:
`gate/` (experiment 1), `slice/bindings/jdbc` (the scratch tree: the build files, the
shim, `org.stuntgres.pq` and the harness of this document, with the observing tests
`SliceObserveTest`, `TextObserveTest`, `CleanerObserveTest`, `ExtraObserveTest`,
`ReferenceObserveTest`), `exp2/`, `exp4.sh`, `exp5/`, `exp10.sh`, `exp-tests/`, and the
observed values in `exp9-observed-<mode>-jdk<n>.txt`. `slice-sources.tgz` is the scratch
tree as an archive. The scratch code is a check of this document, not a starting point:
parts write their files from the sections named in 11.3.

| # | Question | Observation | Consequence |
|---|---|---|---|
| 1 | Do a Go c-shared library and HotSpot coexist? | 86 runs of 60 s on JDK 17.0.13 and 21.0.6 with two libraries: millions of signal-based exceptions, every collector, thread dumps, JFR, `-Xcheck:jni`, `-Xrs`, three kinds of exit, with and without blocked signals: all correct. | Section 2. No remedy needed. |
| 2 | Does the `dlopen`/`dlsym` design work from JNI? | Yes, with `RTLD_NOW` and `RTLD_LOCAL`; missing optional symbols become flags, missing required ones are named; nothing is mapped before the first connect; a second class loader needs its own shim file and shares the target; a second copy of the target is refused. Extraction of a 94.7 MB library: 0.43 s once, 0.06 s afterwards. | 4.2, 8. |
| 3 | Can libpq 18 of the pinned image be loaded in the JVM without touching the system? | Yes: open `liblber-2.5.so.0`, `libldap-2.5.so.0`, then `libpq.so.5.18` by absolute path. | 10.4; dependencies in the load method. |
| 4 | Container life cycle as the harness drives it. | Start to verified about 0.9 s; removed after a pass, a failure, SIGINT and SIGTERM; a killed run is swept by the next; pgjdbc connects. | 10.1, 10.3. |
| 5 | `cmd/stuntgres` the same way; isolation cost. | 3 ms and 3 MB per served database; rolling pool of 64: 5.2 ms per test class; PostgreSQL: 16.5 ms per class for create and drop; pgjdbc and libpq both connect to the server; no engine gap. | 10.3, 10.5. |
| 6 | One JNI call per value or one copy? | Copy 0.56 to 0.58 ms against 3.9 to 5.1 ms for 100,000 values. | Section 6. |
| 7 | Text. | Non-BMP text round-trips in parameters, statement text and column names; an unpaired surrogate becomes `?` (pgjdbc too); NUL in a statement: pgjdbc gets the server error `08P01` (`insufficient data left in message`), this layer raises BAD_ARGUMENT; NUL in a parameter: pgjdbc gets `22021`, reproducible here only in binary format; 10 MiB values both ways; empty string and NULL are distinct everywhere; non-UTF-8 bytes decode to U+FFFD. | 5.5, 9.1, 9.3. For JDBC-DESIGN-CORE and -TYPES: the two pgjdbc answers to NUL characters are recorded in log.txt (experiment 9, reference observations). |
| 8 | Offline Maven build. | Clean build and all modes with `mvn -o` on both JDKs with the pinned plugins; `-q` hides compiler warnings; the host's `mvn` pins its own JDK; selection by `TEST=` and by tag both apply; `make jar` works. | Section 3. |
| 9 | Validation slice. | The scratch pq classes connect, read every result of a statement string, run parameterized and prepared statements, read error fields, drain notices, cancel `pg_sleep` from a second thread, receive notifications, copy in and out, in oracle and server mode under `-Xcheck:jni` on both JDKs with identical observations (except the server's version and the fields `F`, `L`, `R`); the same statements ran through pgjdbc in reference mode. | 11.3 literals. |
| 10 | Notices while the session starts. | `PQconnectdb` prints them to `stderr`; `connectStart` + receiver + `connectPoll` queues them; pgjdbc reports them as connection warnings. | 5.2, 7.2. Open point for libspq. |
| 11 | Forgotten connections; JVM exit. | The cleaner finishes a forgotten connection within milliseconds of a collection; a call in progress keeps its connection; exit with open connections is clean. | 9.1, 9.6. |

Findings that changed the design (also listed in 1.1): connect through
`connectStart`/`connectPoll` with the receiver installed first, and never poll a
descriptor that is not ready; parameter formats; the JDK property of the pom; warn-level
Maven output instead of `-q`; the extra exception kinds; the split of the stand-in
library into weak defaults and per-part overrides.

Not measured: anything that needs libspq (native mode, `PqExt`, `PqCluster` against the
real library, the cost of a cluster per test class, startup notices of libspq); JDK 24
and later (restricted native access); `aarch64`; musl; a result beyond 2 GiB; virtual
threads.

Open points reported with this design:

1. **libspq: notices during session start go to `stderr`** (7.2). The binding cannot
   install a receiver before `SPQconnectStart` has completed the connection.
2. **No SQLSTATE for a failed connection attempt** (9.3): the library gives only text.
   JDBC-DESIGN-CORE needs a rule for the SQLSTATE of `CONNECT_FAILED`.
3. **NUL characters** (experiment 7): pgjdbc's observable answers (`08P01` for a
   statement, `22021` for a parameter) are server errors that this layer can reproduce
   only for parameters, in binary format. JDBC-DESIGN-TYPES and -CORE decide what the
   driver reports.
