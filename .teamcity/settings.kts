import jetbrains.buildServer.configs.kotlin.v2019_2.*
import jetbrains.buildServer.configs.kotlin.v2019_2.buildFeatures.PullRequests
import jetbrains.buildServer.configs.kotlin.v2019_2.buildFeatures.commitStatusPublisher
import jetbrains.buildServer.configs.kotlin.v2019_2.buildFeatures.golang
import jetbrains.buildServer.configs.kotlin.v2019_2.buildFeatures.pullRequests
import jetbrains.buildServer.configs.kotlin.v2019_2.buildSteps.dockerCommand
import jetbrains.buildServer.configs.kotlin.v2019_2.buildSteps.exec
import jetbrains.buildServer.configs.kotlin.v2019_2.buildSteps.powerShell
import jetbrains.buildServer.configs.kotlin.v2019_2.failureConditions.BuildFailureOnMetric
import jetbrains.buildServer.configs.kotlin.v2019_2.failureConditions.failOnMetricChange
import jetbrains.buildServer.configs.kotlin.v2019_2.triggers.vcs

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

version = "2020.2"

val targets = arrayOf(
	"linux/amd64/1.14",
        "linux/amd64/1.15",
        "linux/amd64/1.16",
        "linux/amd64/tip",

        "linux/386/1.16",

        "linux/arm64/1.16",
        "linux/arm64/tip",

        "windows/amd64/1.16",
        "windows/amd64/tip",

        "mac/amd64/1.16",
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
            test.os == os
        }))
    }
    buildType(AggregatorBuild(tests))
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
                    token = "credentialsJSON:5dc93081-e0b2-41e2-b8f0-dea3c96e6426"
                }
                filterAuthorRole = PullRequests.GitHubRoleFilter.EVERYBODY
            }
        }
        commitStatusPublisher {
            vcsRootExtId = "${DslContext.settingsRoot.id}"
            publisher = github {
                githubUrl = "https://api.github.com"
                authType = personalToken {
                    token = "credentialsJSON:48af6e38-536d-4acb-ae2d-2fba57b6f3db"
                }
            }
            param("github_oauth_user", "")
        }
    }

    failureConditions {
        executionTimeoutMin = 60
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

class TestBuild(val os: String, val arch: String, version: String, buildId: AbsoluteId) : BuildType({
    id = buildId
    name = version

    vcs {
        root(DslContext.settingsRoot)
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
                    token = "credentialsJSON:5dc93081-e0b2-41e2-b8f0-dea3c96e6426"
                }
                filterAuthorRole = PullRequests.GitHubRoleFilter.EVERYBODY
            }
        }
        golang {
            testFormat = "json"
        }
    }
})
