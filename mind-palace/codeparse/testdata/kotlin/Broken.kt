package com.example.broken

class Ok {
    fun fine(): Int = 1
}

class Broken {
    fun missingBrace(x: Int {
        return x +
    }
}

fun afterBroken(): String = "still here"
