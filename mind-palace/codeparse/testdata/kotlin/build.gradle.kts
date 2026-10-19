import org.gradle.api.tasks.testing.Test

plugins {
    kotlin("jvm") version "2.0.0"
    application
}

val ktorVersion: String by project

dependencies {
    implementation("io.ktor:ktor-server-core:$ktorVersion")
    testImplementation(kotlin("test"))
}

fun configureTests(task: Test) {
    task.useJUnitPlatform()
}

tasks.withType<Test> {
    configureTests(this)
}
