package com.example.shop.model

/**
 * A customer of the shop.
 *
 * @property id the customer's stable identifier
 */
data class Customer(val id: Long, var name: String, private val secret: String, note: String = "") {
    /** The name shown on receipts. */
    val displayName: String
        get() = name.trim()

    private var visits = 0

    // Records one visit.
    fun visit() {
        visits += 1
    }

    private fun audit(): String = "$id:$visits"

    companion object Factory {
        const val MAX_NAME = 64

        fun of(name: String): Customer = Customer(0, name, "")
    }
}

/** Something that has a price. */
interface Priced {
    val price: Long
    fun discounted(percent: Int): Long
}

fun interface Validator {
    fun validate(value: String): Boolean
}

sealed class Result {
    object Empty : Result()
    class Failure(val message: String) : Result()
}

enum class Currency(val symbol: String) {
    EUR("€"),
    USD("$") {
        override fun format(amount: Long) = "$symbol$amount"
    };

    open fun format(amount: Long): String = "$amount $symbol"
}

object Catalog {
    private val items = mutableMapOf<String, Long>()

    fun price(sku: String): Long? = items[sku]
}

typealias CustomerId = Long

internal const val DEFAULT_CURRENCY = "EUR"

private const val SALT = "x"

/** The customer's initials. */
val Customer.initials: String
    get() = name.split(" ").map { it.first() }.joinToString("")

fun Customer.greeting(): String = "Hello, $name"

private fun <T> List<T>.second(): T? = getOrNull(1)

fun String?.orBlank(): String = this ?: ""
