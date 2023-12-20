import jetbrains.buildServer.configs.kotlin.AbsoluteId
import jetbrains.buildServer.configs.kotlin.BuildType
import jetbrains.buildServer.configs.kotlin.DslContext
import jetbrains.buildServer.configs.kotlin.Project
import jetbrains.buildServer.configs.kotlin.buildFeatures.PullRequests
import jetbrains.buildServer.configs.kotlin.buildFeatures.commitStatusPublisher
import jetbrains.buildServer.configs.kotlin.buildFeatures.golang
import jetbrains.buildServer.configs.kotlin.buildFeatures.pullRequests
import jetbrains.buildServer.configs.kotlin.buildSteps.dockerCommand
import jetbrains.buildServer.configs.kotlin.buildSteps.exec
import jetbrains.buildServer.configs.kotlin.buildSteps.powerShell
import jetbrains.buildServer.configs.kotlin.failureConditions.BuildFailureOnMetric
import jetbrains.buildServer.configs.kotlin.failureConditions.failOnMetricChange
import jetbrains.buildServer.configs.kotlin.project
import jetbrains.buildServer.configs.kotlin.triggers.schedule
import jetbrains.buildServer.configs.kotlin.triggers.vcs
import jetbrains.buildServer.configs.kotlin.version

/*
The settings script is an entry point for defining a TeamCity
project hierarchy. The script should contain a single call to the
project() function with a Project instance or an init function as
an argument.

VcsRoots, BuildTypes, Templates, and subprojects can be
registered inside the project using the vcsRoot(), buildType(),
template(), and subProject() methods respectively.

To debug settings scripts in command-line, run the

    mvnDebug org.jetbrains.teamcity:teamcity-configs-maven-plugin:generate

command and attach your debugger to the port 8000.

To debug in IntelliJ Idea, open the 'Maven Projects' tool window (View
-> Tool Windows -> Maven Projects), find the generate task node
(Plugins -> teamcity-configs -> teamcity-configs:generate), the
'Debug' option is available in the context menu for the task.
*/

version = "2023.05"

val targets = arrayOf(
        "linux/amd64/1.20",
        "linux/amd64/1.21",
        "linux/amd64/1.22",
        "linux/amd64/tip",

        "linux/386/1.22",

        "linux/arm64/1.22",
        "linux/arm64/tip",

        "linux/ppc64le/1.22",

        "windows/amd64/1.22",
        "windows/amd64/tip",

        "mac/amd64/1.22",
        "mac/amd64/tip",

        "mac/arm64/1.22",
        "mac/arm64/tip"
)

project {
    val tests = targets.map { target ->
        val (os, arch, version) = target.split("/")
        TestBuild(os, arch, version, AbsoluteId("Delve_${os}_${arch}_${version.replace('.', '_')}"))
    }
    tests.map { test ->
        test.os
    }.distinct().forEach { os ->
        subProject(OSProject(os, tests.filter { test ->
            test.os == os && test.version != "tip"
        }))
    }
    subProject(TipProject(tests.filter { test ->
        test.version == "tip"
    }))
    buildType(AggregatorBuild(tests.filter { test ->
        test.version != "tip"
    }))
    params {
        param("teamcity.ui.settings.readOnly", "true")
    }
}

class AggregatorBuild(tests: Collection<BuildType>) : BuildType({
    name = "Aggregator"
    type = Type.COMPOSITE

    vcs {
        root(DslContext.settingsRoot)
    }

    triggers {
        vcs {
        }
    }

    dependencies {
        tests.forEach { test ->
            snapshot(test) {
            }
        }
    }

    features {
        pullRequests {
            vcsRootExtId = "${DslContext.settingsRoot.id}"
            provider = github {
                authType = token {
                    token = "credentialsJSON:1312c856-0e13-4b04-8c40-ac26d4a5f700"
                }
                filterAuthorRole = PullRequests.GitHubRoleFilter.EVERYBODY
            }
        }
        commitStatusPublisher {
            vcsRootExtId = "${DslContext.settingsRoot.id}"
            publisher = github {
                githubUrl = "https://api.github.com"
                authType = personalToken {
                    token = "credentialsJSON:1312c856-0e13-4b04-8c40-ac26d4a5f700"
                }
            }
            param("github_oauth_user", "")
        }
    }

    failureConditions {
        executionTimeoutMin = 60
    }
})

class TipProject(tests: List<TestBuild>) : Project({
    id = AbsoluteId("Delve_tip")
    name = "Tip"

    tests.forEach { test ->
        buildType(test)
    }
})

class OSProject(os: String, tests: List<TestBuild>) : Project({
    id = AbsoluteId("Delve_$os")
    name = os.capitalize()

    tests.map { test ->
        test.arch
    }.distinct().forEach { arch ->
        subProject(ArchProject(os, arch, tests.filter { test ->
            test.arch == arch
        }))
    }
})

class ArchProject(os: String, arch: String, tests: List<TestBuild>) : Project({
    id = AbsoluteId("Delve_${os}_${arch}")
    name = arch

    tests.forEach { test ->
        buildType(test)
    }
})

class TestBuild(val os: String, val arch: String, val version: String, buildId: AbsoluteId) : BuildType({
    id = buildId
    name = if (version == "tip") "${os}_${arch}_tip" else version

    vcs {
        root(DslContext.settingsRoot)
        branchFilter = if (version == "tip") {
            """
                +:*
                -:pull/*
                """.trimIndent()
        } else {
            "+:*"
        }
    }

    if (version == "tip") {
        triggers {
            schedule {
                schedulingPolicy = daily {
                    hour = 23
                }
                withPendingChangesOnly = true
                triggerBuild = always()
            }
        }
    }

    failureConditions {
        executionTimeoutMin = 30

        if (version != "tip") {
            failOnMetricChange {
                 metric = BuildFailureOnMetric.MetricType.TEST_COUNT
                 units = BuildFailureOnMetric.MetricUnit.DEFAULT_UNIT
                 comparison = BuildFailureOnMetric.MetricComparison.LESS
                 compareTo = value()
            }
        }
    }

    steps {
        when (os) {
            "linux" -> {
                val dockerArch = when (arch) {
                    "386" -> "i386"
                    "arm64" -> "arm64v8"
                    else -> {
                        arch
                    }
                }
                val dockerPlatformArch = when (arch) {
                    "arm64" -> "arm64/v8"
                    else -> {
                        dockerArch
                    }
                }
                dockerCommand {
                    name = "Pull Ubuntu"
                    commandType = other {
                        subCommand = "pull"
                        commandArgs = "$dockerArch/ubuntu:20.04"
                    }
                }
                dockerCommand {
                    name = "Test"
                    commandType = other {
                        subCommand = "run"
                        commandArgs = """
                        -v %teamcity.build.checkoutDir%:/delve
                        --env TEAMCITY_VERSION=${'$'}TEAMCITY_VERSION
                        --env CI=true
                        --privileged
                        --platform linux/$dockerPlatformArch
                        $dockerArch/ubuntu:20.04
                        /delve/_scripts/test_linux.sh ${"go$version"} $arch
                    """.trimIndent()
                    }
                }
            }
            "windows" -> {
                powerShell {
                    name = "Test"
                    scriptMode = file {
                        path = "_scripts/test_windows.ps1"
                    }
                    param("jetbrains_powershell_scriptArguments", "-version ${"go$version"} -arch $arch")
                }
            }
            "mac" -> {
                exec {
                    name = "Test"
                    path = "_scripts/test_mac.sh"
                    arguments = "${"go$version"} $arch %system.teamcity.build.tempDir%"
                }
            }
        }
    }

    requirements {
        when (arch) {
            "386", "amd64" -> equals("teamcity.agent.jvm.os.arch", if (os == "mac") "x86_64" else "amd64")
            "arm64" -> equals("teamcity.agent.jvm.os.arch", "aarch64")
            "ppc64le" -> equals("teamcity.agent.jvm.os.arch", "ppc64le")
        }
        when (os) {
            "linux" -> {
                matches("teamcity.agent.jvm.os.family", "Linux")
            }
            "windows" -> {
                matches("teamcity.agent.jvm.os.family", "Windows")
            }
            "mac" -> {
                matches("teamcity.agent.jvm.os.family", "Mac OS")
            }
        }
    }

    features {
        pullRequests {
            vcsRootExtId = "${DslContext.settingsRoot.id}"
            provider = github {
                authType = token {
                    token = "credentialsJSON:1312c856-0e13-4b04-8c40-ac26d4a5f700"
                }
                filterAuthorRole = PullRequests.GitHubRoleFilter.EVERYBODY
            }
        }
        golang {
            testFormat = "json"
        }
    }
})
