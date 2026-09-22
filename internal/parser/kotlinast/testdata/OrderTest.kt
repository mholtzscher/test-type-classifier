package shop

import org.junit.jupiter.api.Test
import org.junit.jupiter.params.ParameterizedTest
import org.junit.jupiter.params.provider.CsvSource
import org.testcontainers.containers.PostgreSQLContainer

class OrderTest {
    private val counter = Counter()

    @Test
    fun `creates order`() {
        val total = 1 + 1
        println("total = $total")
    }

    @ParameterizedTest(name = "order {0}")
    @CsvSource("1, 1", "2, 2")
    fun `totals items`(count: Int, expected: Int) {
        // parameterized body
    }
}

object LegacyTest {
    @Test
    fun checksThing() {
        assertGolden("legacy.txt")
    }

    fun notATest() {
    }
}
