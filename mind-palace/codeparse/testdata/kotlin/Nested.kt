@file:JvmName("NestedKt")

package com.example.nested

// No imports: everything here is in this file or the default imports.

class Outer {
    inner class Inner {
        fun inside() = outsideHelper()

        class Deepest {
            val level = 3
        }
    }

    // A nested interface and an anonymous companion object: its members
    // belong to Outer.
    interface Listener {
        fun onEvent(name: String)
    }

    companion object {
        fun create(): Outer = Outer()
    }

    fun register(listener: Listener) {
        // Local declarations are not symbols.
        val local = 1
        fun localHelper() = local + 1
        class LocalType
        val anonymous = object : Listener {
            override fun onEvent(name: String) = println(name)
        }
        anonymous.onEvent("x$localHelper()")
    }
}

fun Outer.Inner.extension(): Int = 1

fun outsideHelper(): Int = 42

@Deprecated("use outsideHelper")
private fun oldHelper(): Int = 41

@Deprecated("inline") private fun oneLineOld() { outsideHelper() }

@Deprecated("inline") internal fun oneLineInternal() {}

fun <T> Box<T>?.orEmpty(): Box<T> = this ?: Box(emptyList())

class Box<T>(val items: List<T>)

@Suppress("unused")
@JvmStatic
internal fun annotated(vararg values: Int) = values.sum()

expect fun platformName(): String

actual class PlatformThing actual constructor() {
    actual fun describe(): String = "jvm"
}

val generated: Map<String, List<Int>> = buildMap {
    put("a", listOf(1))
}
